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

import { ChangeDetectionStrategy, ChangeDetectorRef, Component, EventEmitter, Input, OnChanges, OnDestroy, Output, SimpleChanges } from '@angular/core';
import { FormArray, FormGroup } from '@angular/forms';
import { MatSnackBar } from '@angular/material/snack-bar';
import { HttpErrorResponse } from '@angular/common/http';
import { RadioReferenceLink, RdioScannerAdminService, RRReview, RRReviewItem } from '../../../admin.service';

interface RRChoice {
    id: number;
    name: string;
}

interface SystemChoice {
    id: number;
    label: string;
    systemRef: number;
    talkgroups: number;
}

type ReviewList = 'fill' | 'differ' | 'missing';

const US_COUNTRY_ID = 1;

@Component({
    selector: 'rdio-scanner-admin-system-network',
    templateUrl: './system-network.component.html',
    styleUrls: ['./system-network.component.scss'],
    changeDetection: ChangeDetectionStrategy.OnPush,
    standalone: false,
})
export class RdioScannerAdminSystemNetworkComponent implements OnChanges, OnDestroy {
    @Input() form = new FormGroup({});
    @Input() systems: FormGroup[] = [];
    @Input() systemData: any;
    /** Saved systems, for talkgroup counts of systems whose talkgroups aren't loaded. */
    @Input() savedSystems: any[] = [];

    /** The server changed talkgroups; the parent reloads the config. */
    @Output() serverChanged = new EventEmitter<number>();

    networkFilter = '';

    picking = false;
    rrStates: RRChoice[] = [];
    rrCounties: RRChoice[] = [];
    rrSystems: RRChoice[] = [];
    pickState: RRChoice | null = null;
    pickCounty: RRChoice | null = null;
    pickSystem: RRChoice | null = null;
    rrLoading = '';
    rrError = '';

    review: RRReview | null = null;
    running = false;
    applying = false;
    reviewTab: ReviewList | 'notInRR' = 'fill';
    selected: Record<ReviewList, Set<string>> = { fill: new Set(), differ: new Set(), missing: new Set() };
    missingTargets: Record<number, number> = {};

    private pollTimer: ReturnType<typeof setTimeout> | null = null;
    private loadedFor = 0;

    constructor(
        private adminService: RdioScannerAdminService,
        private cdr: ChangeDetectorRef,
        private snackBar: MatSnackBar,
    ) { }

    ngOnChanges(changes: SimpleChanges): void {
        if (changes['form']) {
            this.picking = false;
            this.networkFilter = '';
            const id = this.systemId;
            if (id !== this.loadedFor) {
                this.loadedFor = id;
                this.review = null;
                this.stopPolling();
                if (id && this.link) {
                    this.loadReview();
                }
            }
        }
    }

    ngOnDestroy(): void {
        this.stopPolling();
    }

    private formValue<T>(name: string): T {
        return this.form.get(name)?.value as unknown as T;
    }

    // ─── Main systems and subsystems ─────────────────────────────────────────

    get systemId(): number {
        const id = this.form.get('id')?.value;
        return typeof id === 'number' && id > 0 ? id : 0;
    }

    get isMain(): boolean {
        return this.form.get('isMain')?.value === true;
    }

    get parentIds(): number[] {
        return this.form.get('parentIds')?.value || [];
    }

    /** Saved main systems this system can go under. */
    get mainChoices(): SystemChoice[] {
        return this.systems
            .filter((s) => s !== this.form && s.value.isMain === true && s.value.id > 0)
            .map((s) => this.choice(s));
    }

    get subsystems(): SystemChoice[] {
        const id = this.systemId;
        if (!id) {
            return [];
        }
        return this.systems
            .filter((s) => s !== this.form && (s.value.parentIds || []).includes(id))
            .map((s) => this.choice(s));
    }

    get parentChoices(): SystemChoice[] {
        const ids = this.parentIds;
        return this.mainChoices.filter((c) => ids.includes(c.id));
    }

    private choice(s: FormGroup): SystemChoice {
        const v = s.value;
        const original = this.systemOriginal(v.id);
        const talkgroups = (v.talkgroups?.length || 0) || (original?.talkgroups?.length || 0);
        return { id: v.id, label: v.label || `System ${v.systemRef}`, systemRef: v.systemRef, talkgroups };
    }

    private systemOriginal(id: number): any {
        return id === this.systemId ? this.systemData : this.savedSystems.find((s) => s.id === id);
    }

    setMain(on: boolean): void {
        const patch: any = { isMain: on };
        if (on) {
            patch.parentIds = [];
            patch.talkgroupNetworks = {};
            patch.noAudioAlertsEnabled = false;
        }
        this.form.patchValue(patch);
        this.form.markAsDirty();
    }

    setParents(ids: number[]): void {
        const networks = { ...(this.formValue<Record<string, number>>('talkgroupNetworks') || {}) };
        for (const key of Object.keys(networks)) {
            if (!ids.includes(networks[key])) {
                delete networks[key];
            }
        }
        this.form.patchValue({ parentIds: ids, talkgroupNetworks: ids.length > 1 ? networks : {} });
        this.form.markAsDirty();
    }

    /** Talkgroups for the network table: the loaded form rows, or the saved list. */
    get talkgroups(): { ref: number; label: string }[] {
        const rows = (this.form.get('talkgroups') as FormArray | null)?.value || [];
        const list = rows.length ? rows : (this.systemData?.talkgroups || []);
        const filter = this.networkFilter.trim().toLowerCase();
        return list
            .map((tg: any) => ({ ref: Number(tg.talkgroupRef), label: tg.label || '' }))
            .filter((tg: { ref: number; label: string }) => tg.ref > 0)
            .filter((tg: { ref: number; label: string }) => !filter || String(tg.ref).includes(filter) || tg.label.toLowerCase().includes(filter))
            .sort((a: { ref: number }, b: { ref: number }) => a.ref - b.ref);
    }

    networkFor(ref: number): number {
        return (this.formValue<Record<string, number>>('talkgroupNetworks') || {})[String(ref)] || 0;
    }

    setNetwork(ref: number, mainId: number): void {
        const networks = { ...(this.formValue<Record<string, number>>('talkgroupNetworks') || {}) };
        if (mainId) {
            networks[String(ref)] = mainId;
        } else {
            delete networks[String(ref)];
        }
        this.form.patchValue({ talkgroupNetworks: networks });
        this.form.markAsDirty();
    }

    // ─── Radio Reference link ────────────────────────────────────────────────

    get link(): RadioReferenceLink | null {
        const v = this.formValue<RadioReferenceLink | null>('radioReference');
        return v && v.systemId > 0 ? v : null;
    }

    async startPicking(): Promise<void> {
        this.picking = true;
        this.rrError = '';
        this.pickSystem = null;
        this.rrCounties = [];
        this.rrSystems = [];
        await this.loadStates();
        const link = this.link;
        if (link) {
            this.pickState = this.rrStates.find((s) => s.id === link.stateId) || null;
            if (this.pickState) {
                await this.loadCounties();
                this.pickCounty = this.rrCounties.find((c) => c.id === link.countyId) || null;
                if (this.pickCounty) {
                    await this.loadSystems();
                    this.pickSystem = this.rrSystems.find((s) => s.id === link.systemId) || null;
                }
            }
        }
        this.cdr.markForCheck();
    }

    private async rrCall(what: string, call: () => Promise<any>): Promise<RRChoice[]> {
        this.rrLoading = what;
        this.rrError = '';
        this.cdr.markForCheck();
        const res = await call();
        this.rrLoading = '';
        if (!res?.success) {
            this.rrError = res?.error || `Couldn't load ${what} from Radio Reference.`;
            this.cdr.markForCheck();
            return [];
        }
        this.cdr.markForCheck();
        return (res.items || []).map((i: any) => ({ id: Number(i.id), name: String(i.name || '') }))
            .sort((a: RRChoice, b: RRChoice) => a.name.localeCompare(b.name));
    }

    async loadStates(): Promise<void> {
        if (!this.rrStates.length) {
            this.rrStates = await this.rrCall('states', () => this.adminService.rrGetStates(US_COUNTRY_ID));
        }
    }

    async onState(state: RRChoice | null): Promise<void> {
        this.pickState = state;
        this.pickCounty = null;
        this.pickSystem = null;
        this.rrSystems = [];
        await this.loadCounties();
    }

    async loadCounties(): Promise<void> {
        this.rrCounties = this.pickState ? await this.rrCall('counties', () => this.adminService.rrGetCounties(this.pickState!.id)) : [];
    }

    async onCounty(county: RRChoice | null): Promise<void> {
        this.pickCounty = county;
        this.pickSystem = null;
        await this.loadSystems();
    }

    async loadSystems(): Promise<void> {
        this.rrSystems = this.pickCounty ? await this.rrCall('systems', () => this.adminService.rrGetSystems(this.pickCounty!.id)) : [];
    }

    usePicked(): void {
        if (!this.pickSystem || !this.pickState || !this.pickCounty) {
            return;
        }
        const link: RadioReferenceLink = {
            systemId: this.pickSystem.id,
            systemName: this.pickSystem.name,
            stateId: this.pickState.id,
            stateName: this.pickState.name,
            countyId: this.pickCounty.id,
            countyName: this.pickCounty.name,
        };
        this.form.patchValue({ radioReference: link });
        this.form.markAsDirty();
        this.picking = false;
        this.review = null;
        this.stopPolling();
    }

    removeLink(): void {
        this.form.patchValue({ radioReference: null });
        this.form.markAsDirty();
        this.review = null;
        this.stopPolling();
    }

    // ─── Updates review ──────────────────────────────────────────────────────

    /** Checks and applies use the saved link and talkgroups, so unsaved edits block them. */
    get savedAndLinked(): boolean {
        return !!this.systemId && !!this.link && !this.form.dirty;
    }

    get underLinkedMain(): string {
        if (this.link || this.isMain) {
            return '';
        }
        const linked = this.systems.filter((s) => this.parentIds.includes(s.value.id) && s.value.radioReference?.systemId > 0);
        return linked.map((s) => s.value.label).join(' and ');
    }

    async loadReview(): Promise<void> {
        const id = this.systemId;
        if (!id) {
            return;
        }
        try {
            const res = await this.adminService.getRRReview(id);
            if (id !== this.systemId) {
                return;
            }
            this.setReview(res.review);
            this.running = res.running;
            if (this.running) {
                this.schedulePoll();
            }
        } catch {
            this.review = null;
        }
        this.cdr.markForCheck();
    }

    private setReview(review: RRReview | null): void {
        this.review = review;
        this.selected = { fill: new Set(), differ: new Set(), missing: new Set() };
        this.missingTargets = {};
        if (review) {
            for (const item of review.fill) {
                this.selected.fill.add(this.key(item));
            }
            for (const item of review.missing) {
                this.missingTargets[item.ref] = item.systemId;
            }
            if (!review.fill.length) {
                this.reviewTab = review.missing.length ? 'missing' : (review.differ.length ? 'differ' : 'fill');
            }
        }
    }

    async checkNow(): Promise<void> {
        try {
            await this.adminService.checkRRUpdates(this.systemId);
            this.running = true;
            this.schedulePoll();
        } catch (error) {
            this.snackBar.open(this.errorText(error, 'Could not start the check.'), 'Close', { duration: 8000 });
        }
        this.cdr.markForCheck();
    }

    private schedulePoll(): void {
        this.stopPolling();
        this.pollTimer = setTimeout(() => this.loadReview(), 5000);
    }

    private stopPolling(): void {
        if (this.pollTimer) {
            clearTimeout(this.pollTimer);
            this.pollTimer = null;
        }
    }

    key(item: RRReviewItem): string {
        return `${item.systemId}:${item.ref}`;
    }

    isSelected(list: ReviewList, item: RRReviewItem): boolean {
        return this.selected[list].has(this.key(item));
    }

    toggle(list: ReviewList, item: RRReviewItem, on: boolean): void {
        const k = this.key(item);
        if (on) {
            this.selected[list].add(k);
        } else {
            this.selected[list].delete(k);
        }
    }

    allSelected(list: ReviewList): boolean {
        const items = this.review?.[list] || [];
        return items.length > 0 && items.every((i) => this.selected[list].has(this.key(i)));
    }

    toggleAll(list: ReviewList, on: boolean): void {
        this.selected[list] = new Set(on ? (this.review?.[list] || []).map((i) => this.key(i)) : []);
    }

    get selectedCount(): number {
        return this.selected.fill.size + this.selected.differ.size + this.selected.missing.size;
    }

    changed(item: RRReviewItem, field: string): boolean {
        return item.changes.includes(field);
    }

    get groupMapEntries(): { category: string; group: string }[] {
        return Object.entries(this.review?.groupMap || {})
            .map(([category, group]) => ({ category, group }))
            .sort((a, b) => a.category.localeCompare(b.category));
    }

    async apply(): Promise<void> {
        if (!this.review || !this.selectedCount) {
            return;
        }
        const picks = (list: ReviewList) => (this.review![list] || [])
            .filter((i) => this.selected[list].has(this.key(i)))
            .map((i) => ({ systemId: list === 'missing' ? (this.missingTargets[i.ref] || i.systemId) : i.systemId, ref: i.ref }));
        this.applying = true;
        this.cdr.markForCheck();
        try {
            const res = await this.adminService.applyRRUpdates({
                systemId: this.systemId,
                fill: picks('fill'),
                differ: picks('differ'),
                missing: picks('missing'),
            });
            this.setReview(res.review);
            this.snackBar.open(`Updated ${res.applied} talkgroup${res.applied === 1 ? '' : 's'} from Radio Reference`, 'Close', { duration: 4000 });
            if (res.applied > 0) {
                this.serverChanged.emit(this.systemId);
            }
        } catch (error) {
            this.snackBar.open(this.errorText(error, 'Could not apply the changes.'), 'Close', { duration: 15000 });
        }
        this.applying = false;
        this.cdr.markForCheck();
    }

    private errorText(error: unknown, fallback: string): string {
        if (error instanceof HttpErrorResponse && error.error?.error) {
            return error.error.error;
        }
        return fallback;
    }
}
