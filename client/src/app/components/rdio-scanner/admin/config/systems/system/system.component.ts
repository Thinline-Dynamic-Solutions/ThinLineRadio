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
import { Component, EventEmitter, Input, Output } from '@angular/core';
import { FormArray, FormControl, FormGroup } from '@angular/forms';
import { RdioScannerAdminService, Group, Tag } from '../../../admin.service';

@Component({
    selector: 'rdio-scanner-admin-system',
    templateUrl: './system.component.html',
    styleUrls: ['./system.component.scss'],
})
export class RdioScannerAdminSystemComponent {
    @Input() form = new FormGroup({});
    @Input() groups: Group[] = [];
    @Input() tags: Tag[] = [];
    @Input() apikeys: any[] = [];

    @Output() remove = new EventEmitter<void>();

    // ─── Expanded row state ────────────────────────────────────────────────────
    expandedTalkgroup: FormGroup | null = null;
    expandedSite: FormGroup | null = null;
    expandedUnit: FormGroup | null = null;

    // ─── Column definitions ────────────────────────────────────────────────────
    talkgroupDisplayedColumns = ['select', 'drag', 'talkgroupRef', 'label', 'name', 'groups', 'tag', 'actions'];
    siteDisplayedColumns      = ['drag', 'siteRef', 'rfss', 'label', 'preferred', 'actions'];
    unitDisplayedColumns      = ['drag', 'unitRef', 'label', 'range', 'actions'];

    // ─── Bulk selection ────────────────────────────────────────────────────────
    selectedTalkgroupIndices: Set<number> = new Set();
    bulkAssignGroupId: number | null = null;
    bulkAssignTagId: number | null = null;

    // ─── Search ────────────────────────────────────────────────────────────────
    talkgroupsSearchTerm = '';
    unitsSearchTerm = '';
    sitesSearchTerm = '';

    // ─── Cached sorted arrays ──────────────────────────────────────────────────
    private _cachedSites:      FormGroup[] = [];
    private _cachedTalkgroups: FormGroup[] = [];
    private _cachedUnits:      FormGroup[] = [];
    private _lastSitesVersion:      number = 0;
    private _lastTalkgroupsVersion: number = 0;
    private _lastUnitsVersion:      number = 0;

    constructor(private adminService: RdioScannerAdminService) { }

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
            arr.clear({ emitEvent: false });
            this._cachedSites.forEach(c => arr.push(c, { emitEvent: false }));
            this._lastSitesVersion = v;
        }
        return this._cachedSites;
    }

    get talkgroups(): FormGroup[] {
        const arr = this.form.get('talkgroups') as FormArray | null;
        if (!arr) return [];
        const v = arr.length;
        if (this._lastTalkgroupsVersion !== v || this._cachedTalkgroups.length !== arr.length) {
            this._cachedTalkgroups = (arr.controls as FormGroup[]).slice().sort((a, b) => {
                const d = (a.value.order || 0) - (b.value.order || 0);
                return d !== 0 ? d : (a.value.talkgroupId || 0) - (b.value.talkgroupId || 0);
            });
            arr.clear({ emitEvent: false });
            this._cachedTalkgroups.forEach(c => arr.push(c, { emitEvent: false }));
            this._lastTalkgroupsVersion = v;
        }
        return this._cachedTalkgroups;
    }

    get units(): FormGroup[] {
        const arr = this.form.get('units') as FormArray | null;
        if (!arr) return [];
        const v = arr.length;
        if (this._lastUnitsVersion !== v || this._cachedUnits.length !== arr.length) {
            this._cachedUnits = (arr.controls as FormGroup[]).slice().sort((a, b) => {
                const d = (a.value.order || 0) - (b.value.order || 0);
                return d !== 0 ? d : (a.value.id || 0) - (b.value.id || 0);
            });
            arr.clear({ emitEvent: false });
            this._cachedUnits.forEach(c => arr.push(c, { emitEvent: false }));
            this._lastUnitsVersion = v;
        }
        return this._cachedUnits;
    }

    // ─── Filtered / paginated ──────────────────────────────────────────────────

    get filteredTalkgroups(): FormGroup[] {
        if (!this.talkgroupsSearchTerm.trim()) return this.talkgroups;
        const s = this.talkgroupsSearchTerm.toLowerCase();
        return this.talkgroups.filter(tg =>
            (tg.value.label || '').toLowerCase().includes(s) ||
            (tg.value.name  || '').toLowerCase().includes(s) ||
            String(tg.value.talkgroupRef).includes(s)
        );
    }

    get filteredSites(): FormGroup[] {
        if (!this.sitesSearchTerm.trim()) return this.sites;
        const s = this.sitesSearchTerm.toLowerCase();
        return this.sites.filter(site => (site.value.label || '').toLowerCase().includes(s));
    }

    get filteredUnits(): FormGroup[] {
        if (!this.unitsSearchTerm.trim()) return this.units;
        const s = this.unitsSearchTerm.toLowerCase();
        return this.units.filter(u =>
            (u.value.label || '').toLowerCase().includes(s) ||
            String(u.value.unitRef).includes(s)
        );
    }

    // ─── Expand / collapse rows ────────────────────────────────────────────────

    toggleTalkgroupExpand(tg: FormGroup): void {
        this.expandedTalkgroup = this.expandedTalkgroup === tg ? null : tg;
    }

    toggleSiteExpand(site: FormGroup): void {
        this.expandedSite = this.expandedSite === site ? null : site;
    }

    toggleUnitExpand(unit: FormGroup): void {
        this.expandedUnit = this.expandedUnit === unit ? null : unit;
    }

    // ─── Helper: look up labels ────────────────────────────────────────────────

    getGroupLabels(groupIds: number[]): string[] {
        if (!groupIds || !groupIds.length) return [];
        return groupIds.map(id => {
            const g = this.groups.find(gr => gr.id === id);
            return (g ? g.label : `#${id}`) as string;
        });
    }

    getTagLabel(tagId: number): string {
        if (!tagId) return '';
        const t = this.tags.find(tg => tg.id === tagId);
        return (t ? t.label : `#${tagId}`) as string;
    }

    // ─── Bulk selection ────────────────────────────────────────────────────────

    get hasSelectedTalkgroups(): boolean { return this.selectedTalkgroupIndices.size > 0; }

    /** True when every currently-visible (filtered) talkgroup is selected. */
    get allTalkgroupsSelected(): boolean {
        const visible = this.filteredTalkgroups;
        if (visible.length === 0) return false;
        return visible.every(tg => {
            const idx = this.talkgroups.indexOf(tg);
            return idx !== -1 && this.selectedTalkgroupIndices.has(idx);
        });
    }

    /** Toggle selection by FormGroup reference — immune to filtered-index drift. */
    toggleTalkgroupSelection(tg: FormGroup): void {
        const idx = this.talkgroups.indexOf(tg);
        if (idx === -1) return;
        if (this.selectedTalkgroupIndices.has(idx)) {
            this.selectedTalkgroupIndices.delete(idx);
        } else {
            this.selectedTalkgroupIndices.add(idx);
        }
    }

    /** Check selection by FormGroup reference — immune to filtered-index drift. */
    isTalkgroupSelected(tg: FormGroup): boolean {
        const idx = this.talkgroups.indexOf(tg);
        return idx !== -1 && this.selectedTalkgroupIndices.has(idx);
    }

    /** Select only the currently visible (filtered) talkgroups. */
    selectAllTalkgroups(): void {
        this.filteredTalkgroups.forEach(tg => {
            const idx = this.talkgroups.indexOf(tg);
            if (idx !== -1) this.selectedTalkgroupIndices.add(idx);
        });
    }

    unselectAllTalkgroups(): void { this.selectedTalkgroupIndices.clear(); }

    bulkAssignGroup(): void {
        if (this.bulkAssignGroupId === null || !this.hasSelectedTalkgroups) return;
        this.selectedTalkgroupIndices.forEach(i => {
            const tg = this.talkgroups[i];
            const ids: number[] = tg.get('groupIds')?.value || [];
            if (!ids.includes(this.bulkAssignGroupId!)) {
                tg.get('groupIds')?.setValue([...ids, this.bulkAssignGroupId]);
                tg.markAsDirty();
            }
        });
        this.form.markAsDirty();
        this.unselectAllTalkgroups();
        this.bulkAssignGroupId = null;
    }

    bulkRemoveGroup(): void {
        if (this.bulkAssignGroupId === null || !this.hasSelectedTalkgroups) return;
        this.selectedTalkgroupIndices.forEach(i => {
            const tg = this.talkgroups[i];
            const ids: number[] = tg.get('groupIds')?.value || [];
            tg.get('groupIds')?.setValue(ids.filter(id => id !== this.bulkAssignGroupId));
            tg.markAsDirty();
        });
        this.form.markAsDirty();
        this.unselectAllTalkgroups();
        this.bulkAssignGroupId = null;
    }

    bulkAssignTag(): void {
        if (this.bulkAssignTagId === null || !this.hasSelectedTalkgroups) return;
        this.selectedTalkgroupIndices.forEach(i => {
            const tg = this.talkgroups[i];
            tg.get('tagId')?.setValue(this.bulkAssignTagId);
            tg.markAsDirty();
        });
        this.form.markAsDirty();
        this.unselectAllTalkgroups();
        this.bulkAssignTagId = null;
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
        const arr = this.form.get('units') as FormArray | null;
        arr?.insert(0, this.adminService.newUnitForm());
        this.form.markAsDirty();
        this._lastUnitsVersion++;
    }

    /** Remove a talkgroup by FormGroup reference — immune to filtered-index drift. */
    removeTalkgroup(tg: FormGroup): void {
        if (this.expandedTalkgroup === tg) this.expandedTalkgroup = null;
        // Deselect it if currently selected
        const selIdx = this.talkgroups.indexOf(tg);
        if (selIdx !== -1) this.selectedTalkgroupIndices.delete(selIdx);
        // Find its actual position in the raw FormArray by reference, not by index
        const arr = this.form.get('talkgroups') as FormArray | null;
        if (!arr) return;
        const arrIdx = (arr.controls as FormGroup[]).indexOf(tg);
        if (arrIdx !== -1) arr.removeAt(arrIdx);
        arr.markAsDirty();
        this._lastTalkgroupsVersion++;
    }

    removeSite(index: number): void {
        if (this.expandedSite === this.sites[index]) this.expandedSite = null;
        const arr = this.form.get('sites') as unknown as FormArray;
        arr?.removeAt(index);
        arr?.markAsDirty();
        this._lastSitesVersion++;
    }

    removeUnit(index: number): void {
        if (this.expandedUnit === this.units[index]) this.expandedUnit = null;
        const arr = this.form.get('units') as unknown as FormArray;
        arr?.removeAt(index);
        arr?.markAsDirty();
        this._lastUnitsVersion++;
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

    dropUnit(event: CdkDragDrop<FormGroup[]>): void {
        if (event.previousIndex === event.currentIndex) return;
        moveItemInArray(event.container.data, event.previousIndex, event.currentIndex);
        event.container.data.forEach((dat, idx) => dat.get('order')?.setValue(idx + 1, { emitEvent: false }));
        this.form.markAsDirty();
        this._lastUnitsVersion++;
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

    onTalkgroupsSearchChange(s: string): void { this.talkgroupsSearchTerm = s; }
    onSitesSearchChange(s: string): void { this.sitesSearchTerm = s; }
    onUnitsSearchChange(s: string): void { this.unitsSearchTerm = s; }
}
