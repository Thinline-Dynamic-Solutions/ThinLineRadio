/*
 * *****************************************************************************
 * Copyright (C) 2019-2024 Chrystian Huot <chrystian@huot.qc.ca>
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

import { CdkDragDrop, moveItemInArray } from '@angular/cdk/drag-drop';
import { Component, EventEmitter, Input, Output, ChangeDetectionStrategy, ChangeDetectorRef, OnInit, OnChanges, OnDestroy, SimpleChanges } from '@angular/core';
import { FormArray, FormControl, FormGroup } from '@angular/forms';
import { Subscription } from 'rxjs';
import { RdioScannerAdminService, Group, Tag } from '../../../admin.service';

@Component({
    selector: 'rdio-scanner-admin-system',
    templateUrl: './system.component.html',
    styleUrls: ['./system.component.scss'],
    changeDetection: ChangeDetectionStrategy.OnPush
})
export class RdioScannerAdminSystemComponent implements OnInit, OnChanges, OnDestroy {
    @Input() form = new FormGroup({});
    @Input() groups: Group[] = [];
    @Input() tags: Tag[] = [];
    @Input() apikeys: any[] = [];
    @Input() systemData: any; // Original system data for lazy loading
    @Input() saving = false;
    @Input() saveSuccess = false;

    @Output() remove = new EventEmitter<void>();
    @Output() save = new EventEmitter<void>();
    @Output() onTalkgroupsLoaded = new EventEmitter<void>();

    // ─── Expanded row state ────────────────────────────────────────────────────
    expandedTalkgroup: FormGroup | null = null;
    expandedSite:      FormGroup | null = null;

    // Units use raw-object display — FormGroup created on demand for editing only
    rawUnits:         any[]          = [];
    expandedRawUnit:  any | null     = null;
    expandedUnitForm: FormGroup|null = null;
    private expandedUnitFormSub: Subscription | null = null;
    private expandedTalkgroupSub: Subscription | null = null;

    // ─── Column definitions ────────────────────────────────────────────────────
    talkgroupDisplayedColumns = ['select', 'drag', 'talkgroupRef', 'label', 'name', 'groups', 'tag', 'alertsEnabled', 'actions'];
    siteDisplayedColumns      = ['drag', 'siteRef', 'rfss', 'label', 'preferred', 'actions'];
    unitDisplayedColumns      = ['drag', 'unitRef', 'label', 'range', 'actions'];

    // ─── Pagination & Performance ──────────────────────────────────────────────
    talkgroupPageSize = 50;
    talkgroupCurrentPage = 0;
    talkgroupsLoaded = false;
    unitPageSize = 50;
    unitCurrentPage = 0;

    // ─── Bulk selection ────────────────────────────────────────────────────────
    /** Selected talkgroups by FormGroup reference — O(1) lookup in the table. */
    selectedTalkgroups: Set<FormGroup> = new Set();
    bulkAssignGroupId: number | null = null;
    bulkAssignTagId: number | null = null;

    // ─── Search ────────────────────────────────────────────────────────────────
    talkgroupsSearchTerm = '';
    unitsSearchTerm = '';
    sitesSearchTerm = '';

    // ─── Cached sorted arrays ──────────────────────────────────────────────────
    private _cachedSites:      FormGroup[] = [];
    private _cachedTalkgroups: FormGroup[] = [];
    private _lastSitesVersion:      number = 0;
    private _lastTalkgroupsVersion: number = 0;

    private tagLabelById = new Map<number, string>();
    private groupLabelById = new Map<number, string>();
    tagsUsedInSystemList: Tag[] = [];

    constructor(
        private adminService: RdioScannerAdminService,
        private cdr: ChangeDetectorRef
    ) { }

    ngOnChanges(changes: SimpleChanges) {
        if (changes['tags'] || changes['groups']) {
            this.rebuildLabelMaps();
        }

        if (changes['systemData']) {
            this.rawUnits = this.systemData?.units ? [...this.systemData.units] : [];
            this.unitCurrentPage = 0;
            this.unitsSearchTerm = '';
            this.expandedUnitFormSub?.unsubscribe();
            this.expandedUnitFormSub = null;
            this.expandedRawUnit = null;
            this.expandedUnitForm = null;
        }

        if (changes['form'] && !changes['form'].firstChange) {
            const tgArray = this.form.get('talkgroups') as FormArray | null;
            this.talkgroupsLoaded = tgArray ? tgArray.length > 0 : false;
            this.talkgroupCurrentPage = 0;
            this.selectedTalkgroups.clear();
            this.talkgroupsSearchTerm = '';

            if (!this.talkgroupsLoaded) {
                setTimeout(() => { this.loadTalkgroupsProgressively(); }, 100);
            }
        }
    }

    ngOnInit() {
        this.rebuildLabelMaps();
        // Initialize raw units instantly from systemData — no FormGroups needed for display
        this.rawUnits = this.systemData?.units ? [...this.systemData.units] : [];

        // Talkgroups still use progressive FormArray loading
        const tgArray = this.form.get('talkgroups') as FormArray | null;
        if (tgArray && tgArray.length > 0) {
            this.talkgroupsLoaded = true;
            this.invalidateTagsUsedInSystem();
        } else {
            setTimeout(() => { this.loadTalkgroupsProgressively(); }, 100);
        }

        if (tgArray && tgArray.length > this.talkgroupPageSize) {
            for (let i = this.talkgroupPageSize; i < tgArray.length; i++) {
                const control = tgArray.at(i);
                if (control) control.disable({ emitEvent: false });
            }
        }
    }

    loadTalkgroupsProgressively() {
        if (this.talkgroupsLoaded || !this.systemData?.talkgroups) {
            return;
        }

        const tgArray = this.form.get('talkgroups') as FormArray | null;
        if (!tgArray || tgArray.length > 0) {
            return;
        }

        const talkgroups = this.systemData.talkgroups;
        const batchSize = 50; // Load 50 talkgroups at a time
        let currentIndex = 0;

        const loadNextBatch = () => {
            const endIndex = Math.min(currentIndex + batchSize, talkgroups.length);
            
            // Load batch
            for (let i = currentIndex; i < endIndex; i++) {
                tgArray.push(this.adminService.newTalkgroupForm(talkgroups[i]), { emitEvent: false });
            }

            currentIndex = endIndex;

            // Check if we're done
            if (currentIndex >= talkgroups.length) {
                this.talkgroupsLoaded = true;
                this.invalidateTagsUsedInSystem();
                this.onTalkgroupsLoaded.emit();
                this.cdr.markForCheck();
            } else {
                // Schedule next batch
                setTimeout(loadNextBatch, 0);
            }
        };

        // Start loading
        loadNextBatch();
    }

    // ─── Sub-array getters ─────────────────────────────────────────────────────

    get sites(): FormGroup[] {
        const arr = this.form.get('sites') as FormArray | null;
        if (!arr) return [];
        const v = arr.length;
        if (this._lastSitesVersion !== v || this._cachedSites.length !== arr.length) {
            this._cachedSites = (arr.controls as FormGroup[]).slice().sort((a, b) => {
                const d = (a.value.order || 0) - (b.value.order || 0);
                return d !== 0 ? d : (a.value.id || 0) - (b.value.id || 0);
            });
            this._lastSitesVersion = v;
        }
        return this._cachedSites;
    }

    get talkgroups(): FormGroup[] {
        if (!this.talkgroupsLoaded) return []; // Don't access until loaded
        const arr = this.form.get('talkgroups') as FormArray | null;
        if (!arr) return [];
        const v = arr.length;
        if (this._lastTalkgroupsVersion !== v || this._cachedTalkgroups.length !== arr.length) {
            this._cachedTalkgroups = (arr.controls as FormGroup[]).slice().sort((a, b) => {
                const d = (a.value.order || 0) - (b.value.order || 0);
                return d !== 0 ? d : (a.value.talkgroupId || 0) - (b.value.talkgroupId || 0);
            });
            this._lastTalkgroupsVersion = v;
        }
        return this._cachedTalkgroups;
    }

    loadTalkgroups() {
        if (!this.talkgroupsLoaded && this.systemData?.talkgroups) {
            const tgArray = this.form.get('talkgroups') as FormArray | null;
            if (tgArray && tgArray.length === 0) {
                this.systemData.talkgroups.forEach((tg: any) => {
                    tgArray.push(this.adminService.newTalkgroupForm(tg), { emitEvent: false });
                });
            }
            this.talkgroupsLoaded = true;
            this.invalidateTagsUsedInSystem();
            this.onTalkgroupsLoaded.emit();
            this.cdr.markForCheck();
        }
    }

    getTalkgroupArrayLength(): number {
        if (this.systemData?.talkgroups) {
            return this.systemData.talkgroups.length;
        }
        const arr = this.form.get('talkgroups') as FormArray | null;
        return arr ? arr.length : 0;
    }

    // ─── Filtered / paginated ──────────────────────────────────────────────────

    get filteredTalkgroups(): FormGroup[] {
        const term = this.talkgroupsSearchTerm.trim();
        if (!term) {
            return this.talkgroups;
        }
        const s = term.toLowerCase();
        return this.talkgroups.filter(tg =>
            (tg.value.label || '').toLowerCase().includes(s) ||
            (tg.value.name || '').toLowerCase().includes(s) ||
            String(tg.value.talkgroupRef).includes(s)
        );
    }

    get paginatedTalkgroups(): FormGroup[] {
        const start = this.talkgroupCurrentPage * this.talkgroupPageSize;
        const end = start + this.talkgroupPageSize;
        return this.filteredTalkgroups.slice(start, end);
    }

    get talkgroupTotalPages(): number {
        return Math.ceil(this.filteredTalkgroups.length / this.talkgroupPageSize);
    }

    get talkgroupPageInfo(): string {
        const total = this.filteredTalkgroups.length;
        if (total === 0) return 'No talkgroups';
        const start = this.talkgroupCurrentPage * this.talkgroupPageSize + 1;
        const end = Math.min((this.talkgroupCurrentPage + 1) * this.talkgroupPageSize, total);
        return `${start}–${end} of ${total}`;
    }

    nextTalkgroupPage(): void {
        if (this.talkgroupCurrentPage < this.talkgroupTotalPages - 1) {
            this.talkgroupCurrentPage++;
            this.collapseExpandedTalkgroup();
            this.cdr.markForCheck();
        }
    }

    prevTalkgroupPage(): void {
        if (this.talkgroupCurrentPage > 0) {
            this.talkgroupCurrentPage--;
            this.collapseExpandedTalkgroup();
            this.cdr.markForCheck();
        }
    }

    goToTalkgroupPage(page: number): void {
        if (page >= 0 && page < this.talkgroupTotalPages) {
            this.talkgroupCurrentPage = page;
            this.collapseExpandedTalkgroup();
            this.cdr.markForCheck();
        }
    }

    // TrackBy functions for performance
    trackByTalkgroupId(index: number, talkgroup: FormGroup): any {
        return talkgroup.value.talkgroupId || talkgroup.value.talkgroupRef || index;
    }

    get filteredSites(): FormGroup[] {
        if (!this.sitesSearchTerm.trim()) return this.sites;
        const s = this.sitesSearchTerm.toLowerCase();
        return this.sites.filter(site => (site.value.label || '').toLowerCase().includes(s));
    }

    // Units operate on rawUnits (plain objects) — no FormGroups created until edit
    get filteredUnits(): any[] {
        const filtered = this.unitsSearchTerm.trim()
            ? this.rawUnits.filter(u => {
                const s = this.unitsSearchTerm.toLowerCase();
                return (u.label || '').toLowerCase().includes(s) ||
                       String(u.unitRef).includes(s);
              })
            : this.rawUnits.slice().sort((a, b) => {
                const d = (a.order || 0) - (b.order || 0);
                return d !== 0 ? d : (a.id || 0) - (b.id || 0);
              });

        const totalPages = Math.ceil(filtered.length / this.unitPageSize);
        if (this.unitCurrentPage >= totalPages && totalPages > 0) {
            this.unitCurrentPage = 0;
        }
        return filtered;
    }

    get paginatedUnits(): any[] {
        const start = this.unitCurrentPage * this.unitPageSize;
        return this.filteredUnits.slice(start, start + this.unitPageSize);
    }

    get unitTotalPages(): number {
        return Math.ceil(this.filteredUnits.length / this.unitPageSize);
    }

    get unitPageInfo(): string {
        const total = this.filteredUnits.length;
        if (total === 0) return 'No units';
        const start = this.unitCurrentPage * this.unitPageSize + 1;
        const end = Math.min((this.unitCurrentPage + 1) * this.unitPageSize, total);
        return `${start}–${end} of ${total}`;
    }

    nextUnitPage(): void {
        if (this.unitCurrentPage < this.unitTotalPages - 1) {
            this.unitCurrentPage++;
            this._closeExpandedUnit();
        }
    }

    prevUnitPage(): void {
        if (this.unitCurrentPage > 0) {
            this.unitCurrentPage--;
            this._closeExpandedUnit();
        }
    }

    onUnitsSearchChange(term: string): void {
        this.unitsSearchTerm = term;
        this.unitCurrentPage = 0;
        this._closeExpandedUnit();
    }

    // ─── Expand / collapse rows ────────────────────────────────────────────────

    toggleTalkgroupExpand(tg: FormGroup): void {
        if (this.expandedTalkgroup === tg) {
            this.collapseExpandedTalkgroup();
            return;
        }
        this.collapseExpandedTalkgroup();
        this.expandedTalkgroup = tg;
        const tagControl = tg.get('tagId');
        if (tagControl) {
            this.expandedTalkgroupSub = tagControl.valueChanges.subscribe(() => {
                this.invalidateTagsUsedInSystem();
                this.cdr.markForCheck();
            });
        }
        this.cdr.markForCheck();
    }

    private collapseExpandedTalkgroup(): void {
        this.expandedTalkgroupSub?.unsubscribe();
        this.expandedTalkgroupSub = null;
        this.expandedTalkgroup = null;
    }

    toggleSiteExpand(site: FormGroup): void {
        this.expandedSite = this.expandedSite === site ? null : site;
    }

    toggleUnitExpand(unit: any): void {
        if (this.expandedRawUnit === unit) {
            this._closeExpandedUnit();
        } else {
            this._openExpandedUnit(unit);
        }
        this.cdr.markForCheck();
    }

    // Open a unit for editing and commit edits live (every keystroke) so the
    // Save button enables immediately — not only when the row is collapsed.
    private _openExpandedUnit(unit: any): void {
        this._closeExpandedUnit();
        this.expandedRawUnit = unit;
        this.expandedUnitForm = this.adminService.newUnitForm(unit);
        this.expandedUnitFormSub = this.expandedUnitForm.valueChanges.subscribe(() => {
            this._commitUnitEdit();
            this.cdr.markForCheck();
        });
    }

    private _closeExpandedUnit(): void {
        this._commitUnitEdit();
        this.expandedUnitFormSub?.unsubscribe();
        this.expandedUnitFormSub = null;
        this.expandedRawUnit = null;
        this.expandedUnitForm = null;
    }

    private _commitUnitEdit(): void {
        if (!this.expandedRawUnit || !this.expandedUnitForm) return;
        const idx = this.rawUnits.indexOf(this.expandedRawUnit);
        if (idx !== -1) {
            Object.assign(this.rawUnits[idx], this.expandedUnitForm.getRawValue());
            if (this.systemData) this.systemData.units = this.rawUnits;
            this.form.markAsDirty();
        }
    }

    ngOnDestroy(): void {
        this.expandedUnitFormSub?.unsubscribe();
        this.expandedTalkgroupSub?.unsubscribe();
    }

    // ─── Helper: look up labels ────────────────────────────────────────────────

    private rebuildLabelMaps(): void {
        this.tagLabelById.clear();
        for (const tag of this.tags) {
            if (tag.id != null && tag.label) {
                this.tagLabelById.set(tag.id, tag.label);
            }
        }
        this.groupLabelById.clear();
        for (const group of this.groups) {
            if (group.id != null && group.label) {
                this.groupLabelById.set(group.id, group.label);
            }
        }
    }

    tagLabel(tagId: number | null | undefined): string {
        if (!tagId) return '';
        return this.tagLabelById.get(tagId) ?? `#${tagId}`;
    }

    getGroupLabels(groupIds: number[]): string[] {
        if (!groupIds?.length) return [];
        return groupIds.map(id => this.groupLabelById.get(id) ?? `#${id}`);
    }

    /** Rebuild tags assigned to talkgroups on this system (bulk rollout pickers). */
    invalidateTagsUsedInSystem(): void {
        const tagIds = new Set<number>();
        if (this.talkgroupsLoaded) {
            for (const tg of this.talkgroups) {
                const id = tg.get('tagId')?.value;
                if (id) tagIds.add(id);
            }
        } else if (this.systemData?.talkgroups) {
            for (const tg of this.systemData.talkgroups) {
                if (tg?.tagId) tagIds.add(tg.tagId);
            }
        }
        this.tagsUsedInSystemList = this.tags.filter(t => t.id != null && tagIds.has(t.id));
    }

    private clampTalkgroupPage(): void {
        const totalPages = this.talkgroupTotalPages;
        if (totalPages > 0 && this.talkgroupCurrentPage >= totalPages) {
            this.talkgroupCurrentPage = 0;
        }
    }

    get toneLearnExpiresLabel(): string {
        const expiresAt: number = this.form.get('autoLearnToneSetsExpiresAt')?.value || 0;
        if (!expiresAt || !this.form.get('autoLearnToneSets')?.value) return '';
        const d = new Date(expiresAt);
        return `Scheduled auto-off: ${d.toLocaleString()}`;
    }

    get unitAliasExpiresLabel(): string {
        const expiresAt: number = this.form.get('autoLearnUnitAliasesExpiresAt')?.value || 0;
        if (!expiresAt || !this.form.get('autoLearnUnitAliases')?.value) return '';
        const d = new Date(expiresAt);
        return `Scheduled auto-off: ${d.toLocaleString()}`;
    }

    // ─── Bulk selection ────────────────────────────────────────────────────────

    get hasSelectedTalkgroups(): boolean { return this.selectedTalkgroups.size > 0; }

    /** True when every currently-visible (filtered) talkgroup is selected. */
    get allTalkgroupsSelected(): boolean {
        const visible = this.filteredTalkgroups;
        if (visible.length === 0) return false;
        return visible.every(tg => this.selectedTalkgroups.has(tg));
    }

    toggleTalkgroupSelection(tg: FormGroup): void {
        if (this.selectedTalkgroups.has(tg)) {
            this.selectedTalkgroups.delete(tg);
        } else {
            this.selectedTalkgroups.add(tg);
        }
        this.cdr.markForCheck();
    }

    isTalkgroupSelected(tg: FormGroup): boolean {
        return this.selectedTalkgroups.has(tg);
    }

    selectAllTalkgroups(): void {
        this.filteredTalkgroups.forEach(tg => this.selectedTalkgroups.add(tg));
        this.cdr.markForCheck();
    }

    unselectAllTalkgroups(): void {
        this.selectedTalkgroups.clear();
        this.cdr.markForCheck();
    }

    bulkAssignGroup(): void {
        if (this.bulkAssignGroupId === null || !this.hasSelectedTalkgroups) return;
        this.selectedTalkgroups.forEach(tg => {
            const ids: number[] = tg.get('groupIds')?.value || [];
            if (!ids.includes(this.bulkAssignGroupId!)) {
                tg.get('groupIds')?.setValue([...ids, this.bulkAssignGroupId]);
                tg.markAsDirty();
            }
        });
        this.form.markAsDirty();
        this.unselectAllTalkgroups();
        this.bulkAssignGroupId = null;
        this.cdr.markForCheck();
    }

    bulkRemoveGroup(): void {
        if (this.bulkAssignGroupId === null || !this.hasSelectedTalkgroups) return;
        this.selectedTalkgroups.forEach(tg => {
            const ids: number[] = tg.get('groupIds')?.value || [];
            tg.get('groupIds')?.setValue(ids.filter(id => id !== this.bulkAssignGroupId));
            tg.markAsDirty();
        });
        this.form.markAsDirty();
        this.unselectAllTalkgroups();
        this.bulkAssignGroupId = null;
        this.cdr.markForCheck();
    }

    bulkAssignTag(): void {
        if (this.bulkAssignTagId === null || !this.hasSelectedTalkgroups) return;
        this.selectedTalkgroups.forEach(tg => {
            tg.get('tagId')?.setValue(this.bulkAssignTagId);
            tg.markAsDirty();
        });
        this.form.markAsDirty();
        this.invalidateTagsUsedInSystem();
        this.unselectAllTalkgroups();
        this.bulkAssignTagId = null;
        this.cdr.markForCheck();
    }

    // ─── CRUD ──────────────────────────────────────────────────────────────────

    addTalkgroup(): void {
        const arr = this.form.get('talkgroups') as FormArray | null;
        arr?.insert(0, this.adminService.newTalkgroupForm());
        this.form.markAsDirty();
        this._lastTalkgroupsVersion++;
    }

    addSite(): void {
        const arr = this.form.get('sites') as FormArray | null;
        arr?.insert(0, this.adminService.newSiteForm());
        this.form.markAsDirty();
        this._lastSitesVersion++;
    }

    addUnit(): void {
        const newUnit = { id: null, label: '', order: 0, unitRef: null, unitFrom: null, unitTo: null };
        this.rawUnits = [newUnit, ...this.rawUnits];
        if (this.systemData) this.systemData.units = this.rawUnits;
        this._openExpandedUnit(newUnit);
        this.form.markAsDirty();
        this.cdr.markForCheck();
    }

    /** Remove a talkgroup by FormGroup reference — immune to filtered-index drift. */
    removeTalkgroup(tg: FormGroup): void {
        if (this.expandedTalkgroup === tg) this.collapseExpandedTalkgroup();
        this.selectedTalkgroups.delete(tg);
        // Find its actual position in the raw FormArray by reference, not by index
        const arr = this.form.get('talkgroups') as FormArray | null;
        if (!arr) return;
        const arrIdx = (arr.controls as FormGroup[]).indexOf(tg);
        if (arrIdx !== -1) arr.removeAt(arrIdx);
        arr.markAsDirty();
        this._lastTalkgroupsVersion++;
        this.invalidateTagsUsedInSystem();
        this.cdr.markForCheck();
    }

    /** Remove by FormGroup reference — table rows are sorted by order, not FormArray index. */
    removeSite(site: FormGroup): void {
        if (this.expandedSite === site) this.expandedSite = null;
        const arr = this.form.get('sites') as FormArray | null;
        if (!arr) return;
        const arrIdx = (arr.controls as FormGroup[]).indexOf(site);
        if (arrIdx !== -1) arr.removeAt(arrIdx);
        arr.markAsDirty();
        this._lastSitesVersion++;
    }

    removeUnit(unit: any): void {
        if (this.expandedRawUnit === unit) {
            this.expandedUnitFormSub?.unsubscribe();
            this.expandedUnitFormSub = null;
            this.expandedRawUnit = null;
            this.expandedUnitForm = null;
        }
        this.rawUnits = this.rawUnits.filter(u => u !== unit);
        if (this.systemData) this.systemData.units = this.rawUnits;
        this.form.markAsDirty();
        this.cdr.markForCheck();
    }

    blacklistTalkgroup(tg: FormGroup): void {
        const talkgroupRef = tg.value.talkgroupRef;
        if (typeof talkgroupRef !== 'number') return;
        const blacklists = this.form.get('blacklists') as FormControl | null;
        blacklists?.setValue(blacklists.value?.trim()
            ? `${blacklists.value},${talkgroupRef}`
            : `${talkgroupRef}`);
        this.removeTalkgroup(tg);
    }

    // ─── Drag & drop ───────────────────────────────────────────────────────────

    dropTalkgroup(event: CdkDragDrop<FormGroup[]>): void {
        if (event.previousIndex === event.currentIndex) return;
        const arr = this.form.get('talkgroups') as FormArray | null;
        if (!arr) return;
        moveItemInArray(event.container.data, event.previousIndex, event.currentIndex);
        event.container.data.forEach((dat, idx) => dat.get('order')?.setValue(idx + 1, { emitEvent: false }));
        const reordered = event.container.data.slice();
        arr.clear({ emitEvent: false });
        reordered.forEach(c => arr.push(c, { emitEvent: false }));
        this.form.markAsDirty();
        this._lastTalkgroupsVersion++;
    }

    dropSite(event: CdkDragDrop<FormGroup[]>): void {
        if (event.previousIndex === event.currentIndex) return;
        moveItemInArray(event.container.data, event.previousIndex, event.currentIndex);
        event.container.data.forEach((dat, idx) => dat.get('order')?.setValue(idx + 1, { emitEvent: false }));
        this.form.markAsDirty();
        this._lastSitesVersion++;
    }

    dropUnit(event: CdkDragDrop<any[]>): void {
        if (event.previousIndex === event.currentIndex) return;
        const page = event.container.data;
        moveItemInArray(page, event.previousIndex, event.currentIndex);
        const offset = this.unitCurrentPage * this.unitPageSize;
        page.forEach((u, idx) => { u.order = offset + idx + 1; });
        if (this.systemData) this.systemData.units = this.rawUnits;
        this.form.markAsDirty();
        this.cdr.markForCheck();
    }

    // ─── Sort ──────────────────────────────────────────────────────────────────

    sortTalkgroupsAlphabetically(): void {
        const arr = this.form.get('talkgroups') as FormArray | null;
        if (!arr || arr.length === 0) return;
        const sorted = arr.controls.slice().sort((a, b) =>
            (a.get('label')?.value || '').toLowerCase().localeCompare(
                (b.get('label')?.value || '').toLowerCase()
            )
        );
        sorted.forEach((c, i) => c.get('order')?.setValue(i + 1, { emitEvent: false }));
        arr.clear({ emitEvent: false });
        sorted.forEach(c => arr.push(c, { emitEvent: false }));
        this.form.markAsDirty();
        this.unselectAllTalkgroups();
        this._lastTalkgroupsVersion++;
    }

    // ─── Error summary helpers ─────────────────────────────────────────────────

    getTalkgroupErrors(tg: FormGroup): string {
        const errors: string[] = [];
        if (tg.get('talkgroupRef')?.hasError('required')) errors.push('ID required');
        else if (tg.get('talkgroupRef')?.hasError('duplicate')) errors.push('Duplicate ID');
        else if (tg.get('talkgroupRef')?.hasError('min')) errors.push('Invalid ID');
        if (tg.get('label')?.hasError('required')) errors.push('Label required');
        if (tg.get('name')?.hasError('required')) errors.push('Name required');
        if (tg.get('groupIds')?.hasError('required')) errors.push('Group required');
        if (tg.get('tagId')?.hasError('required')) errors.push('Tag required');
        return errors.join(', ');
    }

    getTalkgroupsErrorSummary(): string {
        const arr = this.form.get('talkgroups') as FormArray | null;
        if (!arr) return '';
        const n = arr.controls.filter(c => c.invalid).length;
        return n ? `${n} invalid talkgroup${n > 1 ? 's' : ''}` : '';
    }

    // ─── Search handlers ───────────────────────────────────────────────────────

    onTalkgroupsSearchChange(s: string): void {
        this.talkgroupsSearchTerm = s;
        this.talkgroupCurrentPage = 0;
        this.clampTalkgroupPage();
        this.cdr.markForCheck();
    }
    onSitesSearchChange(s: string): void { this.sitesSearchTerm = s; }
}
