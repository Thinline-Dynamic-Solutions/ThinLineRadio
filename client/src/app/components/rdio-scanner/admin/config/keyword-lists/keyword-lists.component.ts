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

import { ChangeDetectorRef, Component, OnDestroy, OnInit } from '@angular/core';
import { FormArray, FormBuilder, FormGroup, Validators } from '@angular/forms';
import { MatDialog } from '@angular/material/dialog';
import { Subscription } from 'rxjs';
import { RdioScannerAdminService } from '../../admin.service';
import { AlertsService } from '../../../alerts/alerts.service';

interface KeywordList {
    id?: number;
    label: string;
    description?: string;
    keywords: string[];
    order: number;
}

@Component({
    selector: 'rdio-scanner-admin-keyword-lists',
    templateUrl: './keyword-lists.component.html',
    styleUrls: ['./keyword-lists.component.scss'],
})
export class RdioScannerAdminKeywordListsComponent implements OnInit, OnDestroy {
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
        private alertsService: AlertsService,
        private formBuilder: FormBuilder,
        private dialog: MatDialog,
        private cdr: ChangeDetectorRef,
    ) {
    }

    ngOnInit(): void {
        this.loadKeywordLists();
    }

    ngOnDestroy(): void {
        if (this.keywordsSubscription) {
            this.keywordsSubscription.unsubscribe();
        }
    }

    loadKeywordLists(): void {
        this.loading = true;
        // Get admin token from admin service - use PIN from config endpoint
        const token = this.adminService.getToken();
        // For admin, we need to check if we should use admin auth
        // For now, use the token as PIN
        this.alertsService.getKeywordLists(token).subscribe({
            next: (lists) => {
                this.keywordLists = lists || [];
                this.loading = false;
                this.cdr.detectChanges();
            },
            error: (error) => {
                console.error('Error loading keyword lists:', error);
                this.keywordLists = [];
                this.loading = false;
                this.cdr.detectChanges();
            },
        });
    }

    startEdit(index: number): void {
        // Clean up previous subscription if any
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
        // Initialize editing keywords and subscribe to changes
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

    saveEdit(): void {
        if (!this.editingForm || this.editingForm.invalid || this.editingIndex === null) {
            return;
        }

        const formValue = this.editingForm.getRawValue();
        const listId = formValue.id;
        const token = this.adminService.getToken();

        if (listId) {
            // Update existing
            this.alertsService.updateKeywordList(listId, formValue, token).subscribe({
                next: () => {
                    this.loadKeywordLists();
                    this.cancelEdit();
                },
                error: (error) => {
                    console.error('Error updating keyword list:', error);
                },
            });
        } else {
            // Create new
            this.alertsService.createKeywordList(formValue, token).subscribe({
                next: () => {
                    this.loadKeywordLists();
                    this.cancelEdit();
                },
                error: (error) => {
                    console.error('Error creating keyword list:', error);
                },
            });
        }
    }

    deleteList(index: number): void {
        const list = this.keywordLists[index];
        if (!list.id) {
            return;
        }

        if (!confirm(`Are you sure you want to delete keyword list "${list.label}"?`)) {
            return;
        }

        const token = this.adminService.getToken();
        this.alertsService.deleteKeywordList(list.id, token).subscribe({
            next: () => {
                this.loadKeywordLists();
            },
            error: (error) => {
                console.error('Error deleting keyword list:', error);
            },
        });
    }

    addNew(): void {
        this.keywordLists.unshift({
            id: undefined,
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

        // Accept both text and JSON files
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
                    // Parse JSON file
                    const jsonData = JSON.parse(text);
                    
                    // Check if it's a categorized JSON (object with arrays)
                    if (typeof jsonData === 'object' && !Array.isArray(jsonData)) {
                        // Ask user if they want to import all categories as separate lists
                        const categories = Object.keys(jsonData);
                        if (categories.length > 1) {
                            const message = `Found ${categories.length} categories in JSON:\n${categories.join(', ')}\n\n` +
                                          `Would you like to:\n` +
                                          `1. Create separate lists for each category (recommended)\n` +
                                          `2. Import all keywords into current list`;
                            
                            const choice = confirm(message + '\n\nClick OK to create separate lists, Cancel to import all into current list');
                            
                            if (choice) {
                                // Create separate lists for each category
                                this.createListsFromJson(jsonData);
                                input.value = '';
                                return;
                            } else {
                                // Import all keywords into current list
                                importedKeywords = [];
                                categories.forEach(category => {
                                    const categoryKeywords = jsonData[category];
                                    if (Array.isArray(categoryKeywords)) {
                                        importedKeywords.push(...categoryKeywords);
                                    }
                                });
                            }
                        } else {
                            // Single category, import directly
                            const categoryKey = categories[0];
                            const categoryKeywords = jsonData[categoryKey];
                            if (Array.isArray(categoryKeywords)) {
                                importedKeywords = categoryKeywords;
                            } else {
                                throw new Error('Invalid JSON structure');
                            }
                        }
                    } else if (Array.isArray(jsonData)) {
                        // Simple array of keywords
                        importedKeywords = jsonData;
                    } else {
                        throw new Error('Invalid JSON structure');
                    }
                } else {
                    // Parse text file - one per line
                    importedKeywords = text
                        .split(/\r?\n/)
                        .map(line => line.trim())
                        .filter(line => line.length > 0);
                }

                // Clean up keywords: trim, remove duplicates
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
                    // Create a new array reference to ensure Angular detects the change
                    keywordsControl.setValue([...updatedKeywords], { emitEvent: true });
                    keywordsControl.markAsDirty();
                    keywordsControl.updateValueAndValidity();
                    // Force change detection
                    this.cdr.markForCheck();
                    // Also trigger detectChanges after a brief delay to ensure UI updates
                    setTimeout(() => {
                        this.cdr.detectChanges();
                    }, 10);
                    alert(`Imported ${newKeywords.length} keyword(s) from file.`);
                }
            } catch (error) {
                alert(`Error parsing file: ${error instanceof Error ? error.message : 'Invalid file format'}`);
            }

            // Reset file input
            input.value = '';
        };

        reader.onerror = () => {
            alert('Error reading file. Please try again.');
            input.value = '';
        };

        reader.readAsText(file);
    }

    createListsFromJson(jsonData: any): void {
        const categories = Object.keys(jsonData);
        const token = this.adminService.getToken();
        let createdCount = 0;
        let skippedCount = 0;

        categories.forEach((categoryKey, index) => {
            const keywords = jsonData[categoryKey];
            if (!Array.isArray(keywords) || keywords.length === 0) {
                skippedCount++;
                return;
            }

            // Format category name: "fire_keywords" -> "Fire Keywords"
            const categoryName = categoryKey
                .split('_')
                .map(word => word.charAt(0).toUpperCase() + word.slice(1))
                .join(' ');

            // Check if list with this name already exists
            const existingList = this.keywordLists.find(list => 
                list.label.toLowerCase() === categoryName.toLowerCase()
            );

            if (existingList) {
                skippedCount++;
                return;
            }

            const listData = {
                label: categoryName,
                description: `Imported from JSON file - ${keywords.length} keywords`,
                keywords: keywords.map((kw: string) => kw.trim()).filter((kw: string) => kw.length > 0),
                order: index
            };

            this.alertsService.createKeywordList(listData, token).subscribe({
                next: () => {
                    createdCount++;
                    if (createdCount + skippedCount === categories.length) {
                        this.loadKeywordLists();
                        const message = `Created ${createdCount} keyword list(s) from JSON file.` +
                                      (skippedCount > 0 ? ` ${skippedCount} category(s) skipped (empty or duplicate names).` : '');
                        alert(message);
                    }
                },
                error: (error) => {
                    console.error('Error creating keyword list:', error);
                    skippedCount++;
                    if (createdCount + skippedCount === categories.length) {
                        alert(`Created ${createdCount} list(s), ${skippedCount} failed or skipped.`);
                    }
                },
            });
        });

        if (categories.length === 0) {
            alert('No valid categories found in JSON file.');
        }
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

