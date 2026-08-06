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
import { FormBuilder, FormGroup, Validators } from '@angular/forms';
import { Subscription } from 'rxjs';
import {
    CallNature,
    CallNaturePhraseLearnStatus,
    CallNaturePhraseSuggestion,
    RdioScannerAdminService,
} from '../../admin.service';

@Component({
    selector: 'rdio-scanner-admin-call-natures',
    templateUrl: './call-natures.component.html',
    styleUrls: ['./call-natures.component.scss'],
    standalone: false
})
export class RdioScannerAdminCallNaturesComponent implements OnInit, OnDestroy {
    callNatures: CallNature[] = [];
    loading = false;
    searchFilter = '';
    editingIndex: number | null = null;
    editingForm: FormGroup | null = null;
    editingPhrases: string[] = [];
    newPhraseText = '';

    learnEnabled = false;
    learnStatus: CallNaturePhraseLearnStatus | null = null;
    suggestions: CallNaturePhraseSuggestion[] = [];
    loadingSuggestions = false;
    scanning = false;
    scanMessage = '';
    private phrasesSubscription?: Subscription;

    constructor(
        private adminService: RdioScannerAdminService,
        private formBuilder: FormBuilder,
        private cdr: ChangeDetectorRef,
    ) {
    }

    ngOnInit(): void {
        void this.loadCallNatures(true);
        void this.loadSuggestions();
    }

    ngOnDestroy(): void {
        this.phrasesSubscription?.unsubscribe();
    }

    get filteredNatures(): CallNature[] {
        const q = this.searchFilter.trim().toUpperCase();
        if (!q) {
            return this.callNatures;
        }
        return this.callNatures.filter((n) => {
            if ((n.label || '').toUpperCase().includes(q)) {
                return true;
            }
            return (n.phrases || []).some((p) => p.toUpperCase().includes(q));
        });
    }

    get readySuggestions(): CallNaturePhraseSuggestion[] {
        return this.suggestions.filter((s) => s.ready);
    }

    get emergingSuggestions(): CallNaturePhraseSuggestion[] {
        return this.suggestions.filter((s) => !s.ready);
    }

    private normalizeNature(nature: CallNature): CallNature {
        return {
            id: nature.id,
            label: nature.label || '',
            phrases: nature.phrases ? [...nature.phrases] : [],
            enabled: nature.enabled !== false,
            order: nature.order ?? 0,
            expireMinutes: nature.expireMinutes ?? 0,
            createdAt: nature.createdAt,
        };
    }

    async loadCallNatures(showSpinner = false): Promise<void> {
        if (showSpinner) {
            this.loading = true;
            this.cdr.markForCheck();
        }
        const natures = await this.adminService.getCallNatures();
        if (natures !== undefined) {
            this.callNatures = natures.map((n) => this.normalizeNature(n));
        }
        this.loading = false;
        this.cdr.markForCheck();
    }

    async loadSuggestions(): Promise<void> {
        this.loadingSuggestions = true;
        this.cdr.markForCheck();
        const res = await this.adminService.getCallNaturePhraseSuggestions();
        if (res) {
            this.learnStatus = res.status;
            this.learnEnabled = !!res.status?.enabled;
            this.suggestions = res.suggestions || [];
        }
        this.loadingSuggestions = false;
        this.cdr.markForCheck();
    }

    async scanHistory(): Promise<void> {
        if (!this.learnEnabled) {
            this.scanMessage = 'Turn on “Suggest phrases from transcripts” above first, then Save if needed.';
            this.cdr.markForCheck();
            return;
        }
        const ok = confirm(
            'Scan recent transcripts for phrase suggestions?\n\n' +
            'This re-reads stored transcriptions from the last 7 days (up to a few hundred calls).\n' +
            'Suggestions are local to your traffic. Nothing is added until you Accept a phrase.',
        );
        if (!ok) {
            return;
        }
        this.scanning = true;
        this.scanMessage = '';
        this.cdr.markForCheck();
        const res = await this.adminService.scanCallNaturePhrases(168, 200);
        if (res) {
            this.suggestions = res.suggestions || [];
            this.scanMessage = res.message || `Scanned ${res.callsScanned} calls.`;
            if (this.learnStatus) {
                this.learnStatus = {
                    ...this.learnStatus,
                    pendingAll: this.suggestions.length,
                    pendingReady: this.readySuggestions.length,
                    minSightings: res.minSightings || this.learnStatus.minSightings,
                };
            }
        } else {
            this.scanMessage = 'Scan failed — check that phrase learning is enabled and try again.';
        }
        this.scanning = false;
        this.cdr.markForCheck();
    }

    async acceptSuggestion(s: CallNaturePhraseSuggestion): Promise<void> {
        const ok = await this.adminService.acceptCallNaturePhraseSuggestion(s.candidateId);
        if (ok) {
            await Promise.all([this.loadCallNatures(), this.loadSuggestions()]);
        }
    }

    async dismissSuggestion(s: CallNaturePhraseSuggestion): Promise<void> {
        const ok = await this.adminService.dismissCallNaturePhraseSuggestion(s.candidateId);
        if (ok) {
            await this.loadSuggestions();
        }
    }

    startEdit(index: number): void {
        this.phrasesSubscription?.unsubscribe();

        this.editingIndex = index;
        const nature = this.callNatures[index];
        this.editingForm = this.formBuilder.group({
            id: [nature.id],
            label: [nature.label || '', Validators.required],
            phrases: [nature.phrases || []],
            enabled: [nature.enabled !== false],
            order: [nature.order || 0],
            expireMinutes: [nature.expireMinutes || 0, [Validators.min(0), Validators.max(10080), Validators.pattern(/^\d+$/)]],
        });
        this.editingPhrases = [...(nature.phrases || [])];
        const phrasesControl = this.editingForm.get('phrases');
        if (phrasesControl) {
            this.phrasesSubscription = phrasesControl.valueChanges.subscribe((phrases: string[]) => {
                this.editingPhrases = phrases ? [...phrases] : [];
                this.cdr.markForCheck();
            });
        }
    }

    cancelEdit(): void {
        this.phrasesSubscription?.unsubscribe();
        this.phrasesSubscription = undefined;
        this.editingIndex = null;
        this.editingForm = null;
        this.editingPhrases = [];
        this.newPhraseText = '';
    }

    async saveEdit(): Promise<void> {
        if (!this.editingForm || this.editingIndex === null) {
            return;
        }

        // Commit a phrase typed into the "Add phrase" box but not yet added via
        // Enter/Add, so clicking Save directly doesn't silently drop it.
        this.addPhraseFromInput();

        if (this.editingForm.invalid) {
            return;
        }

        const formValue = this.editingForm.getRawValue();
        const natureId = formValue.id;
        const payload = {
            ...formValue,
            label: (formValue.label || '').toUpperCase().trim(),
            phrases: (formValue.phrases || []).map((p: string) => p.toUpperCase().trim()).filter((p: string) => p.length > 0),
            expireMinutes: Math.max(0, Math.round(Number(formValue.expireMinutes) || 0)),
        };

        const ok = natureId
            ? await this.adminService.updateCallNature(natureId, payload)
            : await this.adminService.createCallNature(payload);

        if (ok) {
            await this.loadCallNatures();
            this.cancelEdit();
        }
    }

    async deleteNature(index: number): Promise<void> {
        const nature = this.callNatures[index];
        if (!nature.id) {
            return;
        }
        if (!confirm(`Delete call nature "${nature.label}"?`)) {
            return;
        }
        const ok = await this.adminService.deleteCallNature(nature.id);
        if (ok) {
            await this.loadCallNatures();
        }
    }

    addNew(): void {
        this.callNatures.unshift({
            label: '',
            phrases: [],
            enabled: true,
            order: 0,
            expireMinutes: 0,
        });
        this.startEdit(0);
    }

    formatExpire(minutes: number | undefined): string {
        const m = minutes || 0;
        if (m <= 0) {
            return '';
        }
        if (m < 60) {
            return `${m} min`;
        }
        const hours = Math.floor(m / 60);
        const rest = m % 60;
        return rest > 0 ? `${hours}h ${rest}m` : `${hours}h`;
    }

    addPhraseFromInput(): void {
        if (!this.editingForm || !this.newPhraseText.trim()) {
            return;
        }
        const phrasesControl = this.editingForm.get('phrases');
        if (!phrasesControl) {
            return;
        }
        const phrase = this.newPhraseText.trim().toUpperCase();
        const phrases: string[] = phrasesControl.value || [];
        if (!phrases.includes(phrase)) {
            phrasesControl.setValue([...phrases, phrase]);
            phrasesControl.markAsDirty();
        }
        this.newPhraseText = '';
    }

    removePhrase(form: FormGroup, phrase: string): void {
        const phrasesControl = form.get('phrases');
        if (!phrasesControl) {
            return;
        }
        const phrases = phrasesControl.value || [];
        const index = phrases.indexOf(phrase);
        if (index >= 0) {
            phrases.splice(index, 1);
            phrasesControl.setValue([...phrases]);
        }
    }

    indexOfNature(nature: CallNature): number {
        return this.callNatures.indexOf(nature);
    }
}
