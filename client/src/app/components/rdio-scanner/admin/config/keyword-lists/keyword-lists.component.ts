/*
 * *****************************************************************************
 * Copyright (C) 2025 Thinline Dynamic Solutions
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with this program.  If not, see <http://www.gnu.org/licenses/>
 * ****************************************************************************
 */

import { ChangeDetectorRef, Component, Input, OnDestroy, OnInit } from '@angular/core';
import { FormBuilder, FormGroup, Validators } from '@angular/forms';
import { Subscription } from 'rxjs';
import { KeywordList, RdioScannerAdminService } from '../../admin.service';

@Component({
    selector: 'rdio-scanner-admin-keyword-lists',
    templateUrl: './keyword-lists.component.html',
    styleUrls: ['./keyword-lists.component.scss'],
})
export class RdioScannerAdminKeywordListsComponent implements OnInit, OnDestroy {
    /**
     * When set (from full admin config `keywordLists`), the panel hydrates
     * immediately with no extra HTTP round-trip.
     */
    @Input() initialLists: KeywordList[] | null | undefined;

    keywordLists: KeywordList[] = [];
    loading = false;
    editingIndex: number | null = null;
    editingForm: FormGroup | null = null;
    editingKeywords: string[] = [];
    newKeywordText = '';
    private keywordsSubscription?: Subscription;

    get currentEditingKeywords(): string[] {
        return this.editingKeywords;
    }

    constructor(
        private adminService: RdioScannerAdminService,
        private formBuilder: FormBuilder,
        private cdr: ChangeDetectorRef,
    ) {
    }

    ngOnInit(): void {
        if (this.tryHydrateFromInitialLists()) {
            return;
        }
        void this.loadKeywordLists(true);
    }

    ngOnDestroy(): void {
        if (this.keywordsSubscription) {
            this.keywordsSubscription.unsubscribe();
        }
    }

    private tryHydrateFromInitialLists(): boolean {
        if (!Array.isArray(this.initialLists)) {
            return false;
        }
        this.keywordLists = this.initialLists.map(list => this.normalizeList(list));
        this.loading = false;
        return true;
    }

    private normalizeList(list: KeywordList): KeywordList {
        return {
            id: list.id,
            label: list.label || '',
            description: list.description || '',
            keywords: list.keywords ? [...list.keywords] : [],
            order: list.order ?? 0,
        };
    }

    async loadKeywordLists(showSpinner = false): Promise<void> {
        if (showSpinner) {
            this.loading = true;
            this.cdr.markForCheck();
        }
        const lists = await this.adminService.getKeywordLists();
        if (lists !== undefined) {
            this.keywordLists = lists.map(list => this.normalizeList(list));
        }
        this.loading = false;
        this.cdr.markForCheck();
    }

    startEdit(index: number): void {
        if (this.keywordsSubscription) {
            this.keywordsSubscription.unsubscribe();
        }

        this.editingIndex = index;
        const list = this.keywordLists[index];
        this.editingForm = this.formBuilder.group({
            id: [list.id],
            label: [list.label || '', Validators.required],
            description: [list.description || ''],
            keywords: [list.keywords || []],
            order: [list.order || 0],
        });
        this.editingKeywords = [...(list.keywords || [])];
        const keywordsControl = this.editingForm.get('keywords');
        if (keywordsControl) {
            this.keywordsSubscription = keywordsControl.valueChanges.subscribe((keywords: string[]) => {
                this.editingKeywords = keywords ? [...keywords] : [];
                this.cdr.markForCheck();
            });
        }
    }

    cancelEdit(): void {
        if (this.keywordsSubscription) {
            this.keywordsSubscription.unsubscribe();
            this.keywordsSubscription = undefined;
        }
        this.editingIndex = null;
        this.editingForm = null;
        this.editingKeywords = [];
    }

    async saveEdit(): Promise<void> {
        if (!this.editingForm || this.editingForm.invalid || this.editingIndex === null) {
            return;
        }

        const formValue = this.editingForm.getRawValue();
        const listId = formValue.id;

        const ok = listId
            ? await this.adminService.updateKeywordList(listId, formValue)
            : await this.adminService.createKeywordList(formValue);

        if (ok) {
            await this.loadKeywordLists();
            this.cancelEdit();
        }
    }

    async deleteList(index: number): Promise<void> {
        const list = this.keywordLists[index];
        if (!list.id) {
            return;
        }

        if (!confirm(`Are you sure you want to delete keyword list "${list.label}"?`)) {
            return;
        }

        const ok = await this.adminService.deleteKeywordList(list.id);
        if (ok) {
            await this.loadKeywordLists();
        }
    }

    addNew(): void {
        this.keywordLists.unshift({
            label: '',
            description: '',
            keywords: [],
            order: 0,
        });
        this.startEdit(0);
    }

    addKeywordFromInput(): void {
        if (!this.editingForm || !this.newKeywordText.trim()) return;
        const keywordsControl = this.editingForm.get('keywords');
        if (!keywordsControl) return;
        const keyword = this.newKeywordText.trim();
        const keywords: string[] = keywordsControl.value || [];
        if (!keywords.includes(keyword)) {
            keywordsControl.setValue([...keywords, keyword]);
            keywordsControl.markAsDirty();
        }
        this.newKeywordText = '';
    }

    addKeyword(form: FormGroup): void {
        const keywordsControl = form.get('keywords');
        if (!keywordsControl) return;
        const keywords = keywordsControl.value || [];
        const newKeyword = prompt('Enter keyword:');
        if (newKeyword && newKeyword.trim() && !keywords.includes(newKeyword.trim())) {
            keywords.push(newKeyword.trim());
            keywordsControl.setValue(keywords);
        }
    }

    importKeywordsFromFile(form: FormGroup, event: Event): void {
        const keywordsControl = form.get('keywords');
        if (!keywordsControl) {
            return;
        }

        const input = event.target as HTMLInputElement;
        const file = input.files?.[0];
        if (!file) {
            return;
        }

        const isJson = file.name.endsWith('.json') || file.type === 'application/json';
        const isText = file.name.endsWith('.txt') || file.type.startsWith('text/');

        if (!isJson && !isText) {
            alert('Please select a text file (.txt) or JSON file (.json)');
            input.value = '';
            return;
        }

        const reader = new FileReader();
        reader.onload = (e) => {
            const text = e.target?.result as string;
            if (!text) {
                return;
            }

            try {
                let importedKeywords: string[] = [];

                if (isJson) {
                    const jsonData = JSON.parse(text);

                    if (typeof jsonData === 'object' && !Array.isArray(jsonData)) {
                        const categories = Object.keys(jsonData);
                        if (categories.length > 1) {
                            const message = `Found ${categories.length} categories in JSON:\n${categories.join(', ')}\n\n` +
                                          `Would you like to:\n` +
                                          `1. Create separate lists for each category (recommended)\n` +
                                          `2. Import all keywords into current list`;

                            const choice = confirm(message + '\n\nClick OK to create separate lists, Cancel to import all into current list');

                            if (choice) {
                                void this.createListsFromJson(jsonData);
                                input.value = '';
                                return;
                            } else {
                                importedKeywords = [];
                                categories.forEach(category => {
                                    const categoryKeywords = jsonData[category];
                                    if (Array.isArray(categoryKeywords)) {
                                        importedKeywords.push(...categoryKeywords);
                                    }
                                });
                            }
                        } else {
                            const categoryKey = categories[0];
                            const categoryKeywords = jsonData[categoryKey];
                            if (Array.isArray(categoryKeywords)) {
                                importedKeywords = categoryKeywords;
                            } else {
                                throw new Error('Invalid JSON structure');
                            }
                        }
                    } else if (Array.isArray(jsonData)) {
                        importedKeywords = jsonData;
                    } else {
                        throw new Error('Invalid JSON structure');
                    }
                } else {
                    importedKeywords = text
                        .split(/\r?\n/)
                        .map(line => line.trim())
                        .filter(line => line.length > 0);
                }

                importedKeywords = importedKeywords
                    .map(kw => kw.trim())
                    .filter(kw => kw.length > 0)
                    .filter((keyword, index, self) => self.indexOf(keyword) === index);

                const existingKeywords = keywordsControl.value || [];
                const newKeywords = importedKeywords.filter(kw => !existingKeywords.includes(kw));

                if (newKeywords.length === 0) {
                    alert('No new keywords to add. All keywords from the file already exist.');
                } else {
                    const updatedKeywords = [...existingKeywords, ...newKeywords];
                    keywordsControl.setValue([...updatedKeywords], { emitEvent: true });
                    keywordsControl.markAsDirty();
                    keywordsControl.updateValueAndValidity();
                    this.cdr.markForCheck();
                    setTimeout(() => {
                        this.cdr.detectChanges();
                    }, 10);
                    alert(`Imported ${newKeywords.length} keyword(s) from file.`);
                }
            } catch (error) {
                alert(`Error parsing file: ${error instanceof Error ? error.message : 'Invalid file format'}`);
            }

            input.value = '';
        };

        reader.onerror = () => {
            alert('Error reading file. Please try again.');
            input.value = '';
        };

        reader.readAsText(file);
    }

    async createListsFromJson(jsonData: any): Promise<void> {
        const categories = Object.keys(jsonData);
        let createdCount = 0;
        let skippedCount = 0;

        for (const [index, categoryKey] of categories.entries()) {
            const keywords = jsonData[categoryKey];
            if (!Array.isArray(keywords) || keywords.length === 0) {
                skippedCount++;
                continue;
            }

            const categoryName = categoryKey
                .split('_')
                .map(word => word.charAt(0).toUpperCase() + word.slice(1))
                .join(' ');

            const existingList = this.keywordLists.find(list =>
                (list.label || '').toLowerCase() === categoryName.toLowerCase()
            );

            if (existingList) {
                skippedCount++;
                continue;
            }

            const listData = {
                label: categoryName,
                description: `Imported from JSON file - ${keywords.length} keywords`,
                keywords: keywords.map((kw: string) => kw.trim()).filter((kw: string) => kw.length > 0),
                order: index,
            };

            const ok = await this.adminService.createKeywordList(listData);
            if (ok) {
                createdCount++;
            } else {
                skippedCount++;
            }
        }

        if (categories.length === 0) {
            alert('No valid categories found in JSON file.');
            return;
        }

        await this.loadKeywordLists();
        const message = `Created ${createdCount} keyword list(s) from JSON file.` +
                      (skippedCount > 0 ? ` ${skippedCount} category(s) skipped (empty or duplicate names).` : '');
        alert(message);
    }

    removeKeyword(form: FormGroup, keyword: string): void {
        const keywordsControl = form.get('keywords');
        if (!keywordsControl) {
            return;
        }
        const keywords = keywordsControl.value || [];
        const index = keywords.indexOf(keyword);
        if (index >= 0) {
            keywords.splice(index, 1);
            keywordsControl.setValue(keywords);
        }
    }
}
