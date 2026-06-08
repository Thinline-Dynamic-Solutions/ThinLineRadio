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

import { CdkDragDrop, moveItemInArray } from '@angular/cdk/drag-drop';
import { ChangeDetectorRef, Component, Input, OnChanges, QueryList, SimpleChanges, ViewChildren } from '@angular/core';
import { FormArray, FormControl, FormGroup } from '@angular/forms';
import { MatExpansionPanel } from '@angular/material/expansion';
import { MatSnackBar } from '@angular/material/snack-bar';
import { RdioScannerAdminService } from '../../admin.service';

interface DirwatchTalkgroupOption {
    id: number;
    label: string;
}

/** Systems with more talkgroups than this require typing in the talkgroup search box. */
const TALKGROUP_INLINE_LIMIT = 50;
const TALKGROUP_SEARCH_RESULT_LIMIT = 200;

@Component({
    selector: 'rdio-scanner-admin-dirwatch',
    templateUrl: './dirwatch.component.html',
    styleUrls: ['./dirwatch.component.scss'],
})
export class RdioScannerAdminDirwatchComponent implements OnChanges {
    @Input() form: FormArray | undefined;
    @Input() rawSystems: any[] | undefined;

    saving = false;
    showValidationErrors = false;

    /** Per-dirwatch talkgroup search text (keyed by form index). */
    talkgroupSearch: Record<number, string> = {};

    private talkgroupOptionsCache = new Map<number, DirwatchTalkgroupOption[]>();
    private filteredTalkgroupsCache = new Map<string, DirwatchTalkgroupOption[]>();
    private talkgroupSelectOpened = new Set<number>();

    @ViewChildren(MatExpansionPanel) private panels: QueryList<MatExpansionPanel> | undefined;

    constructor(
        private adminService: RdioScannerAdminService,
        private cdr: ChangeDetectorRef,
        private snackBar: MatSnackBar,
    ) { }

    get dirwatches(): FormGroup[] {
        const controls = this.form?.controls as FormGroup[] | undefined;
        if (!controls?.length) {
            return [];
        }

        return controls.slice().sort((a, b) => (a.value.order || 0) - (b.value.order || 0));
    }

    get sites(): FormGroup[][] {
        return this.systems.reduce((sites, system) => {
            const faSites = system.get('sites') as FormArray;

            sites[system.value.id] = faSites.controls as FormGroup[];

            return sites;
        }, [] as FormGroup[][]);
    }

    get systems(): FormGroup[] {
        const systems = this.form?.root.get('systems') as FormArray;

        return systems?.controls as FormGroup[] || [];
    }

    ngOnChanges(changes: SimpleChanges): void {
        if (changes['rawSystems']) {
            this.talkgroupOptionsCache.clear();
            this.filteredTalkgroupsCache.clear();
        }

        if (this.form) {
            this.dirwatches.forEach((control) => this.registerOnChanges(control));
        }
    }

    add(): void {
        const dirwatch = this.adminService.newDirwatchForm({
            delay: 2000,
            deleteAfter: true,
        });

        dirwatch.markAsDirty({ onlySelf: false });

        this.registerOnChanges(dirwatch);

        this.form?.insert(0, dirwatch);

        this.form?.markAsDirty();
    }

    closeAll(): void {
        this.panels?.forEach((panel) => panel.close());
    }

    drop(event: CdkDragDrop<FormGroup[]>): void {
        if (event.previousIndex !== event.currentIndex) {
            moveItemInArray(event.container.data, event.previousIndex, event.currentIndex);

            event.container.data.forEach((dat, idx) => dat.get('order')?.setValue(idx + 1, { emitEvent: false }));

            this.form?.markAsDirty();
            this.saveAll(false);
        }
    }

    remove(index: number): void {
        this.form?.removeAt(index);

        this.form?.markAsDirty();
        this.saveAll(false);
    }

    getSitesForSystem(systemId: number | null | undefined): FormGroup[] {
        if (!systemId) {
            return [];
        }

        return this.sites[systemId] || [];
    }

    getTalkgroupCount(systemId: number | null | undefined): number {
        if (!systemId) {
            return 0;
        }

        const cached = this.talkgroupOptionsCache.get(systemId);
        if (cached) {
            return cached.length;
        }

        const systemForm = this.systems.find((s) => s.value.id === systemId);
        const faTalkgroups = systemForm?.get('talkgroups') as FormArray | undefined;
        if (faTalkgroups && faTalkgroups.controls.length > 0) {
            return faTalkgroups.controls.filter((tg) => (tg as FormGroup).value.id > 0).length;
        }

        const rawSystem = this.rawSystems?.find((s: any) => s.id === systemId);
        return rawSystem?.talkgroups?.length || 0;
    }

    getTalkgroupsForSystem(systemId: number | null | undefined, load = false): DirwatchTalkgroupOption[] {
        if (!systemId) {
            return [];
        }

        const cached = this.talkgroupOptionsCache.get(systemId);
        if (cached) {
            return cached;
        }

        if (!load) {
            return [];
        }

        const systemForm = this.systems.find((s) => s.value.id === systemId);
        const faTalkgroups = systemForm?.get('talkgroups') as FormArray | undefined;

        let options: DirwatchTalkgroupOption[];
        if (faTalkgroups && faTalkgroups.controls.length > 0) {
            options = (faTalkgroups.controls as FormGroup[])
                .map((tg) => ({ id: tg.value.id, label: tg.value.label }))
                .filter((tg) => tg.id > 0);
        } else {
            const rawSystem = this.rawSystems?.find((s: any) => s.id === systemId);
            options = (rawSystem?.talkgroups || [])
                .filter((tg: any) => tg.id > 0)
                .map((tg: any) => ({ id: tg.id, label: tg.label }));
        }

        this.talkgroupOptionsCache.set(systemId, options);
        return options;
    }

    private ensureTalkgroupsLoaded(systemId: number | null | undefined): void {
        if (!systemId || this.talkgroupOptionsCache.has(systemId)) {
            return;
        }

        this.getTalkgroupsForSystem(systemId, true);
        this.filteredTalkgroupsCache.clear();
        this.cdr.markForCheck();
    }

    getFilteredTalkgroups(
        systemId: number | null | undefined,
        dirwatchIndex: number,
        selectedTalkgroupId: number | null | undefined,
    ): DirwatchTalkgroupOption[] {
        if (!systemId) {
            return [];
        }

        const query = (this.talkgroupSearch[dirwatchIndex] || '').trim().toLowerCase();
        const shouldLoadAll = this.talkgroupSelectOpened.has(dirwatchIndex) || !!query;
        const cacheKey = `${systemId}:${dirwatchIndex}:${query}:${selectedTalkgroupId ?? ''}:${shouldLoadAll}`;
        const cached = this.filteredTalkgroupsCache.get(cacheKey);
        if (cached) {
            return cached;
        }

        const all = shouldLoadAll
            ? this.getTalkgroupsForSystem(systemId, true)
            : this.getTalkgroupsForSystem(systemId, this.getTalkgroupCount(systemId) <= TALKGROUP_INLINE_LIMIT);
        let results: DirwatchTalkgroupOption[];

        if (!query) {
            if (all.length <= TALKGROUP_INLINE_LIMIT && all.length > 0) {
                results = all;
            } else if (selectedTalkgroupId) {
                results = all.filter((tg) => tg.id === selectedTalkgroupId);
                if (!results.length) {
                    const selected = this.findRawTalkgroup(systemId, selectedTalkgroupId);
                    if (selected) {
                        results = [selected];
                    }
                }
            } else {
                results = [];
            }
        } else {
            const searchable = all.length
                ? all
                : this.getTalkgroupsForSystem(systemId, true);
            results = searchable
                .filter((tg) =>
                    tg.label?.toLowerCase().includes(query) ||
                    String(tg.id).includes(query),
                )
                .slice(0, TALKGROUP_SEARCH_RESULT_LIMIT);
        }

        if (selectedTalkgroupId && !results.some((tg) => tg.id === selectedTalkgroupId)) {
            const selected = all.find((tg) => tg.id === selectedTalkgroupId)
                || this.findRawTalkgroup(systemId, selectedTalkgroupId);
            if (selected) {
                results = [selected, ...results];
            }
        }

        this.filteredTalkgroupsCache.set(cacheKey, results);
        return results;
    }

    talkgroupSearchHint(systemId: number | null | undefined): string {
        const count = this.getTalkgroupCount(systemId);
        if (count <= TALKGROUP_INLINE_LIMIT) {
            return '';
        }

        return `Type to search ${count.toLocaleString()} talkgroups`;
    }

    onTalkgroupSelectOpened(dirwatchIndex: number, opened: boolean): void {
        if (opened) {
            this.talkgroupSelectOpened.add(dirwatchIndex);
            this.ensureTalkgroupsLoaded(this.dirwatches[dirwatchIndex]?.value?.systemId);
        } else {
            this.talkgroupSelectOpened.delete(dirwatchIndex);
            delete this.talkgroupSearch[dirwatchIndex];
            this.filteredTalkgroupsCache.clear();
        }
    }

    onTalkgroupSearch(dirwatchIndex: number, event: Event): void {
        const value = (event.target as HTMLInputElement).value || '';
        this.talkgroupSearch[dirwatchIndex] = value;
        this.ensureTalkgroupsLoaded(this.dirwatches[dirwatchIndex]?.value?.systemId);
        this.filteredTalkgroupsCache.clear();
        this.cdr.markForCheck();
    }

    trackByTalkgroupId(_index: number, talkgroup: DirwatchTalkgroupOption): number {
        return talkgroup.id;
    }

    trackBySiteId(_index: number, site: FormGroup): number {
        return site.value.id;
    }

    showFieldError(dirwatch: FormGroup, name: string): boolean {
        const control = dirwatch.get(name);
        if (!control || control.disabled) {
            return false;
        }

        return control.invalid && (control.touched || this.showValidationErrors);
    }

    rowHasError(dirwatch: FormGroup, names: string[]): boolean {
        return names.some((name) => this.showFieldError(dirwatch, name));
    }

    /**
     * API-driven save: PUT /api/admin/dirwatch with the full list. The server
     * restarts the directory watchers after persisting. Auto-invoked for
     * reorder/remove; the Save button covers directory/mask text edits.
     */
    async saveAll(showToast = true): Promise<void> {
        if (!this.form) return;

        this.touchAndValidateAll();

        if (this.form.invalid) {
            this.showValidationErrors = true;
            this.cdr.markForCheck();
            setTimeout(() => this.openInvalidPanels());
            if (showToast) {
                this.snackBar.open('Fix the highlighted fields before saving.', 'Close', { duration: 4000 });
            }
            return;
        }

        this.saving = true;
        const updated = await this.adminService.saveDirwatch(this.form.getRawValue());
        this.saving = false;

        if (updated) {
            this.showValidationErrors = false;
            this.form.markAsPristine();
            if (showToast) {
                this.snackBar.open('Dirwatch saved', 'Close', { duration: 1500 });
            }
        } else if (showToast) {
            this.snackBar.open('Failed to save dirwatch. Please try again.', 'Close', { duration: 4000 });
        }
    }

    private touchAndValidateAll(): void {
        this.dirwatches.forEach((dirwatch) => {
            dirwatch.markAllAsTouched();
            Object.keys(dirwatch.controls).forEach((key) => {
                dirwatch.get(key)?.updateValueAndValidity({ emitEvent: false });
            });
        });
    }

    private openInvalidPanels(): void {
        this.panels?.forEach((panel, index) => {
            if (this.dirwatches[index]?.invalid) {
                panel.open();
            }
        });
    }

    private registerOnChanges(control: FormGroup): void {
        const mask = control.get('mask') as FormControl;
        const type = control.get('type') as FormControl;
        const systemId = control.get('systemId') as FormControl;

        mask.valueChanges.subscribe(() => this.validateIds(control));
        type.valueChanges.subscribe(() => {
            this.validateIds(control);
            control.get('mask')?.updateValueAndValidity({ emitEvent: false });
        });
        systemId?.valueChanges.subscribe(() => {
            control.get('talkgroupId')?.setValue(null, { emitEvent: false });
            this.filteredTalkgroupsCache.clear();
            this.validateIds(control);
        });
    }

    private findRawTalkgroup(systemId: number, talkgroupId: number): DirwatchTalkgroupOption | undefined {
        const rawSystem = this.rawSystems?.find((s: any) => s.id === systemId);
        const raw = rawSystem?.talkgroups?.find((tg: any) => tg.id === talkgroupId);
        if (!raw?.id) {
            return undefined;
        }

        return { id: raw.id, label: raw.label };
    }

    private validateIds(control: FormGroup): void {
        const systemId = control.get('systemId');
        const talkgroupId = control.get('talkgroupId');
        const mask = control.get('mask');

        systemId?.updateValueAndValidity({ emitEvent: false });
        talkgroupId?.updateValueAndValidity({ emitEvent: false });
        mask?.updateValueAndValidity({ emitEvent: false });
    }
}
