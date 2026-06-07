/*
 * *****************************************************************************
 * Copyright (C) 2019-2024 Chrystian Huot <chrystian@huot.qc.ca>
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

import { Component, ElementRef, EventEmitter, Input, Output, ViewChild } from '@angular/core';
import { FormArray, FormBuilder, FormGroup, Validators } from '@angular/forms';
import { MatSelectChange } from '@angular/material/select';
import { MatSnackBar } from '@angular/material/snack-bar';
import { finalize } from 'rxjs/operators';
import { RdioScannerAdminService, Group, Tag, ToneHistorySuggestion } from '../../../admin.service';
import { RdioScannerToneSet } from '../../../../rdio-scanner';

@Component({
    selector: 'rdio-scanner-admin-talkgroup',
    templateUrl: './talkgroup.component.html',
    styleUrls: ['./talkgroup.component.scss'],
})
export class RdioScannerAdminTalkgroupComponent {
    @Input() form: FormGroup | undefined;

    @Output() blacklist = new EventEmitter<void>();

    @Output() remove = new EventEmitter<void>();

    @ViewChild('twoToneFileInput') twoToneFileInput?: ElementRef<HTMLInputElement>;
    @ViewChild('csvFileInput') csvFileInput?: ElementRef<HTMLInputElement>;

    importingToneSets = false;
    syncingToneSets   = false;
    syncToneSetsStatus = '';
    syncSelectedIds   = new Set<string>();

    analyzingToneHistory = false;
    toneHistoryStatus = '';
    toneHistorySuggestions: ToneHistorySuggestion[] = [];
    toneHistoryPartialPatterns: { patternDesc: string; callCount: number }[] = [];
    toneHistoryCallsRequired = 3;

    get groups(): Group[] {
        return this.form?.root.get('groups')?.value as Group[];
    }

    get tags(): Tag[] {
        return this.form?.root.get('tags')?.value as Tag[];
    }

    get apikeys(): any[] {
        return this.form?.root.get('apikeys')?.value as any[] || [];
    }

    get systemId(): number | undefined {
        const systemForm = this.form?.parent?.parent;
        const id = systemForm?.get('id')?.value;
        return typeof id === 'number' && id > 0 ? id : undefined;
    }

    constructor(
        private adminService: RdioScannerAdminService,
        private formBuilder: FormBuilder,
        private snackBar: MatSnackBar,
    ) {
    }

    getToneSets(): FormArray {
        if (!this.form) {
            return this.formBuilder.array([]) as FormArray;
        }
        let toneSetsArray = this.form.get('toneSets') as FormArray;
        if (!toneSetsArray) {
            toneSetsArray = this.formBuilder.array([]);
            this.form.addControl('toneSets', toneSetsArray);
        }
        return toneSetsArray;
    }

    addToneSet(toneSet?: Partial<RdioScannerToneSet>): void {
        const toneSetForm = this.formBuilder.group({
            id: [toneSet?.id || this.generateToneSetId()],
            label: [toneSet?.label || '', Validators.required],
            aToneFrequency: [toneSet?.aTone?.frequency ?? null],
            aToneMinDuration: [toneSet?.aTone?.minDuration ?? null],
            aToneMaxDuration: [toneSet?.aTone?.maxDuration ?? null],
            bToneFrequency: [toneSet?.bTone?.frequency ?? null],
            bToneMinDuration: [toneSet?.bTone?.minDuration ?? null],
            bToneMaxDuration: [toneSet?.bTone?.maxDuration ?? null],
            longToneFrequency: [toneSet?.longTone?.frequency ?? null],
            longToneMinDuration: [toneSet?.longTone?.minDuration ?? null],
            longToneMaxDuration: [toneSet?.longTone?.maxDuration ?? null],
            tolerance: [toneSet?.tolerance ?? 10],
            minDuration: [toneSet?.minDuration ?? null],
            // TonesToActive downstream forwarding (per tone set)
            downstreamEnabled: [(toneSet as any)?.downstreamEnabled ?? false],
            downstreamURL: [(toneSet as any)?.downstreamURL ?? ''],
            downstreamAPIKey: [(toneSet as any)?.downstreamAPIKey ?? ''],
        });
        this.getToneSets().push(toneSetForm);
    }

    removeToneSet(index: number): void {
        this.getToneSets().removeAt(index);
    }

    triggerToneImport(format: ToneImportFormat): void {
        if (format === 'twotone') {
            this.twoToneFileInput?.nativeElement.click();
        } else {
            this.csvFileInput?.nativeElement.click();
        }
    }

    async handleToneImport(event: Event, format: ToneImportFormat): Promise<void> {
        const input = event.target as HTMLInputElement;
        const file = input?.files?.[0];
        if (!file || !this.form) {
            return;
        }

        let content = '';
        try {
            content = await file.text();
        } catch {
            this.snackBar.open('Unable to read the selected file', '', { duration: 4000 });
            input.value = '';
            return;
        }

        this.importingToneSets = true;
        this.adminService.importToneSets(format, content)
            .pipe(finalize(() => {
                this.importingToneSets = false;
                if (input) {
                    input.value = '';
                }
            }))
            .subscribe({
                next: (response) => {
                    const imported = response?.toneSets || [];
                    if (imported.length > 0) {
                        this.appendImportedToneSets(imported);
                        const label = format === 'twotone' ? 'TwoToneDetect' : 'CSV';
                        this.snackBar.open(`Imported ${imported.length} tone set${imported.length === 1 ? '' : 's'} from ${label}`, '', { duration: 4000 });
                    } else {
                        this.snackBar.open('No tone sets were found in the selected file', '', { duration: 5000 });
                    }

                    if (response?.warnings?.length) {
                        this.snackBar.open(response.warnings.join(' '), 'Dismiss', { duration: 6000 });
                    }
                },
                error: (error) => {
                    const message = error?.error?.error || 'Failed to import tone sets';
                    this.snackBar.open(message, '', { duration: 6000 });
                },
            });
    }

    allToneSetsForwardingEnabled(): boolean {
        const controls = this.getToneSets().controls;
        return controls.length > 0 && controls.every(c => c.get('downstreamEnabled')?.value === true);
    }

    setAllToneSetsForwarding(enabled: boolean): void {
        this.getToneSets().controls.forEach(ctrl => {
            ctrl.get('downstreamEnabled')?.setValue(enabled);
        });
    }

    isToneSetSelected(id: string): boolean {
        return this.syncSelectedIds.has(id);
    }

    toggleToneSetSelection(id: string, checked: boolean): void {
        if (checked) {
            this.syncSelectedIds.add(id);
        } else {
            this.syncSelectedIds.delete(id);
        }
    }

    selectAllToneSets(): void {
        const all = this.getToneSets().controls.every(c => this.syncSelectedIds.has(c.get('id')?.value));
        if (all) {
            // All already selected — deselect all (toggle behaviour)
            this.syncSelectedIds.clear();
        } else {
            this.getToneSets().controls.forEach(c => {
                const id = c.get('id')?.value;
                if (id) this.syncSelectedIds.add(id);
            });
        }
    }

    syncToneSetsToDownstream(url: string, apiKey: string): void {
        if (!url || this.syncingToneSets) return;

        const toneSets = this.getToneSets().controls
            .filter(c => this.syncSelectedIds.has(c.get('id')?.value))
            .map(c => ({
                id:    c.get('id')?.value    || '',
                label: c.get('label')?.value || '',
            }))
            .filter(ts => ts.label);

        if (toneSets.length === 0) {
            this.snackBar.open('No tone sets selected to sync', '', { duration: 3000 });
            return;
        }

        this.syncingToneSets    = true;
        this.syncToneSetsStatus = '';

        this.adminService.syncToneSets(url, apiKey, toneSets)
            .pipe(finalize(() => { this.syncingToneSets = false; }))
            .subscribe({
                next: () => {
                    this.syncToneSetsStatus = `✓ Synced ${toneSets.length} tone set${toneSets.length === 1 ? '' : 's'}`;
                    this.snackBar.open('Tone sets synced to TonesToActive', '', { duration: 4000 });
                },
                error: (err) => {
                    const msg = err?.error?.error || 'Sync failed — check URL and API key';
                    this.syncToneSetsStatus = `✗ ${msg}`;
                    this.snackBar.open(msg, '', { duration: 5000 });
                },
            });
    }

    analyzeToneHistory(): void {
        if (!this.form || this.analyzingToneHistory) {
            return;
        }

        const talkgroupId = this.form.get('id')?.value;
        const systemId = this.systemId;
        if (!talkgroupId || !systemId) {
            this.snackBar.open('Save this talkgroup first so it has a database ID', '', { duration: 5000 });
            return;
        }

        this.analyzingToneHistory = true;
        this.toneHistoryStatus = '';
        this.toneHistorySuggestions = [];
        this.toneHistoryPartialPatterns = [];

        this.adminService.analyzeToneHistory(systemId, talkgroupId)
            .pipe(finalize(() => { this.analyzingToneHistory = false; }))
            .subscribe({
                next: (response) => {
                    this.toneHistoryCallsRequired = response?.callsRequired ?? 3;
                    this.toneHistorySuggestions = response?.suggestions || [];
                    this.toneHistoryPartialPatterns = response?.partialPatterns || [];
                    if (this.toneHistorySuggestions.length > 0) {
                        this.toneHistoryStatus = `Found ${this.toneHistorySuggestions.length} pattern${this.toneHistorySuggestions.length === 1 ? '' : 's'} (≥${this.toneHistoryCallsRequired} calls each) in ${response.callsScanned} calls`;
                        this.snackBar.open(this.toneHistoryStatus, '', { duration: 5000 });
                    } else {
                        this.toneHistoryStatus = response?.message || `No patterns with at least ${this.toneHistoryCallsRequired} matching calls`;
                        this.snackBar.open(this.toneHistoryStatus, '', { duration: 6000 });
                    }
                },
                error: (error) => {
                    const message = error?.error?.error || 'Tone history analysis failed';
                    this.toneHistoryStatus = message;
                    this.snackBar.open(message, '', { duration: 6000 });
                },
            });
    }

    addSuggestedToneSet(suggestion: ToneHistorySuggestion): void {
        if (!this.form || !suggestion?.toneSet) {
            return;
        }
        if (!this.form.get('toneDetectionEnabled')?.value) {
            this.form.get('toneDetectionEnabled')?.setValue(true);
        }
        this.addToneSet({
            ...suggestion.toneSet,
            label: suggestion.label || suggestion.toneSet.label,
        });
        this.toneHistorySuggestions = this.toneHistorySuggestions.filter((s) => s !== suggestion);
        this.snackBar.open(`Added tone set "${suggestion.label}" — save config to persist`, '', { duration: 5000 });
    }

    private appendImportedToneSets(toneSets: RdioScannerToneSet[]): void {
        if (!this.form) {
            return;
        }

        if (!this.form.get('toneDetectionEnabled')?.value) {
            this.form.get('toneDetectionEnabled')?.setValue(true);
        }

        toneSets.forEach((toneSet) => this.addToneSet(toneSet));
    }

    private generateToneSetId(): string {
        return `tone-set-${Date.now()}-${Math.random().toString(36).substr(2, 9)}`;
    }
}

type ToneImportFormat = 'twotone' | 'csv';
