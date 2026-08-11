/*
 * *****************************************************************************
 * Copyright (C) 2019-2022 Chrystian Huot <chrystian.huot@saubeo.solutions>
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

import { AfterViewInit, ChangeDetectorRef, Component, ElementRef, OnDestroy, ViewChild, ChangeDetectionStrategy } from '@angular/core';
import { FormBuilder } from '@angular/forms';
import { MatDatepicker } from '@angular/material/datepicker';
import { BehaviorSubject } from 'rxjs';
import { resolveUnitLabelForSrc } from '../unit-utils';
import {
    RdioScannerCall,
    RdioScannerConfig,
    RdioScannerEvent,
    RdioScannerLivefeedMode,
    RdioScannerPlaybackList,
    RdioScannerSearchOptions,
    RdioScannerSystem,
    RdioScannerTalkgroup,
} from '../rdio-scanner';
import { RdioScannerService } from '../rdio-scanner.service';
import { FavoritesService } from '../favorites.service';
import { TagColorService } from '../tag-color.service';

/**
 * Cross-session persistence for Archive (Playback) filters and pagination.
 *
 * Issue #185: keep system / talkgroup / page selection when bouncing between
 * Live and Playback. Stored as labels (not array indices) so it survives
 * config reloads and reorderings; a `favoriteKey` of `${systemId}:${talkgroupId}`
 * captures the favorites picker selection in a config-independent way.
 */
interface PlaybackPrefs {
    systemLabel?: string;
    talkgroupLabel?: string;
    /** Multi-select talkgroup labels (same system). */
    talkgroupLabels?: string[];
    groupLabel?: string;
    tagLabel?: string;
    favoriteKey?: string;
    date?: string;
    /** HH:MM, 24h. Only meaningful when `date` is also set. */
    time?: string;
    sort?: number;
}
const PLAYBACK_PREFS_STORAGE_KEY = 'rdio-scanner-playback-prefs';

@Component({
    selector: 'rdio-scanner-search',
    styleUrls: ['./search.component.scss'],
    templateUrl: './search.component.html',
    changeDetection: ChangeDetectionStrategy.Eager,
    standalone: false
})
export class RdioScannerSearchComponent implements AfterViewInit, OnDestroy {
    call: RdioScannerCall | undefined;
    callPending: number | undefined;

    /** Columns for archive results table (keep in sync with template). Stable refs for MatTable. */
    private readonly archiveColumnsBase = [
        'control',
        'date',
        'time',
        'system',
        'alpha',
        'tgid',
        'source',
        'name',
        'duration',
    ];
    private readonly archiveColumnsAdmin = [
        'control',
        'date',
        'time',
        'callid',
        'system',
        'alpha',
        'tgid',
        'source',
        'name',
        'duration',
    ];

    get archiveTableColumns(): string[] {
        return this.isSystemAdmin ? this.archiveColumnsAdmin : this.archiveColumnsBase;
    }

    get isSystemAdmin(): boolean {
        return this.rdioScannerService.isSystemAdmin();
    }

    form: any;

    /** Snapshot of saved prefs read once at construction; consumed when config arrives. */
    private pendingPrefs: PlaybackPrefs | null = null;
    /** Avoid re-applying prefs every time a config event arrives. */
    private prefsApplied = false;

    constructor(
        private rdioScannerService: RdioScannerService,
        private ngChangeDetectorRef: ChangeDetectorRef,
        private ngFormBuilder: FormBuilder,
        private favoritesService: FavoritesService,
        private tagColorService: TagColorService,
    ) {
        this.pendingPrefs = this.loadPrefs();

        this.form = this.ngFormBuilder.group({
            date: [null],
            group: [-1],
            sort: this.pendingPrefs?.sort ?? -1,
            system: [-1],
            tag: [-1],
            talkgroups: [[] as number[]],
            favorite: [-1],
        });

        // Intentionally do NOT restore `date` / `time` from saved prefs.
        //
        // Persisting the date across reloads turned out to be flaky: the
        // initial search that fires from applyPendingPrefs() runs during
        // the same tick as the first WS Config event, which races with PIN
        // hydration on the server. When it loses the race the LCL reply
        // never comes back and the Archive view sits on "Loading calls…"
        // for ~12s (until the watchdog clears it) before the user can
        // interact again. The other filters (system / talkgroup / sort /
        // page) are config-dependent and apply *after* config has fully
        // arrived, so they don't have the same race. Starting the date
        // clean every reload eliminates the wait without changing how
        // saving / restoring works for those filters.
        //
        // Strip date / time from the in-memory prefs snapshot too, so
        // applyPendingPrefs() can't see them later, and overwrite the
        // localStorage copy so subsequent loads don't keep restoring a
        // stale date the user can't easily clear.
        if (this.pendingPrefs && (this.pendingPrefs.date || this.pendingPrefs.time)) {
            this.pendingPrefs = { ...this.pendingPrefs, date: undefined, time: undefined };
            try {
                if (typeof localStorage !== 'undefined') {
                    localStorage.setItem(PLAYBACK_PREFS_STORAGE_KEY, JSON.stringify(this.pendingPrefs));
                }
            } catch { /* quota or disabled storage — silently skip */ }
        }

        this.eventSubscription = this.rdioScannerService.event.subscribe((event: RdioScannerEvent) => this.eventHandler(event));

        // The `event` emitter is a fire-and-forget EventEmitter (no replay buffer).
        // Whenever this component is re-mounted after the initial config event
        // already fired (e.g. toggling the view, opening a sidenav for the first
        // time), the filter dropdowns would otherwise sit empty until the next
        // config push.
        //
        // Seed the *data-only* fields synchronously so option arrays populate
        // before the first render. Anything that mutates the form (which would
        // trigger valueChanges -> refreshFilters -> form.disable -> CD cycles
        // before the view is initialised) is deferred to a microtask.
        this.seedFromCachedConfig();
    }

    private seedFromCachedConfig(): void {
        // `RdioScannerService.config` is initialised as a non-null default
        // (empty systems/groups/tags) before the websocket ever connects, so a
        // simple truthy check would always pass. We only want to seed when the
        // service has *actually received* a config from the server — otherwise
        // we'd burn the one-shot `prefsApplied` token against empty options
        // and the real config event would never restore the saved filters,
        // leaving the page stuck on "Loading calls…".
        const cached = this.rdioScannerService.getConfig();
        if (!cached || !cached.systems || cached.systems.length === 0) {
            return;
        }

        this.config = cached;
        this.optionsGroup = Object.keys(cached.groups || []).sort((a, b) => a.localeCompare(b));
        this.optionsSystem = (cached.systems || []).map((system) => system.label);
        this.optionsTag = Object.keys(cached.tags || []).sort((a, b) => a.localeCompare(b));
        this.time12h = cached.time12hFormat || false;

        // Side-effecting work that touches the form (and therefore CD) waits
        // until after the constructor returns and the view is bound.
        Promise.resolve().then(() => {
            try {
                this.loadFavorites();
                this.applyPendingPrefs();
                if (this.optionsSystem.length === 1 && this.form.value.system === -1) {
                    this.form.patchValue({ system: 0 }, { emitEvent: false });
                    this.refreshFilters();
                }
            } catch (e) {
                // Non-fatal: the next real config event will retry these paths.
                console.warn('search.component: deferred seed failed', e);
            }
        });
    }

    livefeedOnline = false;
    livefeedPlayback = false;

    playbackList: RdioScannerPlaybackList | undefined;

    optionsGroup: string[] = [];
    optionsSystem: string[] = [];
    optionsTag: string[] = [];
    optionsTalkgroup: string[] = [];
    optionsFavorites: Array<{systemId: number, talkgroupId: number, label: string}> = [];

    paused = false;

    results = new BehaviorSubject<RdioScannerCall[]>([]);
    resultsPending = false;
    /** True while appending the next scroll-loaded batch (keeps table visible). */
    loadingMore = false;

    time12h = false;

    private config: RdioScannerConfig | undefined;

    private eventSubscription: any;

    /** Server batch size — same 200-record pages the mat-paginator used to fetch. */
    private readonly limit = 200;

    private offset = 0;

    /** Rows loaded from the server so far (grows one batch at a time on scroll). */
    private accumulatedResults: RdioScannerCall[] = [];
    private loadedOffsets: Set<number> = new Set();
    hasMoreResults = false;
    /** Avoid auto-loading every batch when the load-more sentinel is visible on first paint. */
    private scrollLoadEnabled = false;
    private lastSearchOptions: RdioScannerSearchOptions | null = null;
    private isRefreshing = false; // Guard flag to prevent recursive calls
    private formChangeTimeout: any = null; // Debounce timer for form changes
    private isExecutingFormChange = false; // Guard to prevent multiple simultaneous form change executions
    private lastRequestId: string | null = null; // Track last request to prevent duplicates
    /**
     * Watchdog timer that force-clears `resultsPending` if the WS response
     * never arrives (e.g. server dropped the message due to a full Send
     * channel, query timeout, or a race where the LCL was sent before the
     * server finished hydrating client.User). Without this, the user is
     * permanently stuck on "Loading calls…" — even clicking Clear date or
     * picking a new date silently no-ops because `formChangeHandler` returns
     * early while `resultsPending` is true. 12s gives a slow archive query
     * room to breathe while still recovering before the user reaches for F5.
     */
    private resultsPendingWatchdog: any = null;
    private readonly resultsPendingWatchdogMs = 12000;
    private loadMoreObserver: IntersectionObserver | undefined;

    @ViewChild('archiveScroll') private archiveScroll: ElementRef<HTMLElement> | undefined;
    @ViewChild('loadMoreSentinel') private loadMoreSentinel: ElementRef<HTMLElement> | undefined;
    @ViewChild('datePicker') private datePicker: MatDatepicker<Date> | undefined;
    
    selectedDate: Date | null = null;
    /**
     * Time-of-day filter (HH:MM, 24h). When set in combination with
     * `selectedDate`, the search Date sent to the backend is shifted to that
     * exact moment instead of midnight. Whether the backend further narrows
     * results by time depends on its filter implementation; if not, the
     * selection is still useful as a scrub-to point.
     */
    selectedTime: string | null = null;

    get showInitialSearchOverlay(): boolean {
        return this.resultsPending && this.accumulatedResults.length === 0;
    }

    get showLoadMoreIndicator(): boolean {
        return this.loadingMore || (this.resultsPending && this.accumulatedResults.length > 0);
    }

    get loadedResultCount(): number {
        return this.accumulatedResults.length;
    }

    download(id: number): void {
        this.rdioScannerService.loadAndDownload(id);
    }

    formChangeHandler(): void {
        if (this.livefeedPlayback) {
            this.rdioScannerService.stopPlaybackMode();
        }

        // NOTE: we intentionally do NOT bail when `resultsPending` is true.
        // Doing so used to leave the UI permanently stuck on "Loading calls…"
        // whenever a previous WS request got lost (full Send channel, slow
        // archive query, race with PIN auth) — every subsequent click on
        // Clear date / Pick date silently no-op'd until the user reloaded the
        // page. The debounce below + the dedupe inside `searchCalls` are
        // enough to keep rapid input from spamming the websocket.

        // Debounce form changes to prevent repeated requests (especially for date input)
        // Clear any existing timeout to reset the debounce timer
        if (this.formChangeTimeout) {
            clearTimeout(this.formChangeTimeout);
            this.formChangeTimeout = null;
        }

        this.formChangeTimeout = setTimeout(() => {
            this._executeFormChange();
            this.formChangeTimeout = null;
        }, 1000);
    }

    private _executeFormChange(): void {
        // Re-entrancy guard only — `resultsPending` is intentionally NOT
        // checked here so a fresh user filter change can override a stuck
        // in-flight search (see comment in `formChangeHandler`).
        if (this.isExecutingFormChange) {
            return;
        }

        // If a previous search got stranded (no playbackList response ever
        // arrived), reset its bookkeeping so the new search isn't blocked
        // by `if (this.resultsPending) return;` inside `searchCalls`.
        if (this.resultsPending) {
            this.clearResultsWatchdog();
            this.resultsPending = false;
            this.lastRequestId = null;
            try { this.form.enable(); } catch { /* form may already be enabled */ }
        }

        this.isExecutingFormChange = true;
        
        try {

        // Reset accumulation for new search (matching Flutter app behavior)
        this.accumulatedResults = [];
        this.loadedOffsets.clear();
        this.hasMoreResults = false;
        this.scrollLoadEnabled = false;
        this.lastSearchOptions = null;
        this.lastRequestId = null; // Reset request ID for new search
        this.offset = 0;
        this.loadingMore = false;
        
        // Clear display immediately when filters change
        this.results.next([]);
        this.playbackList = undefined;

        this.refreshFilters();

        // Don't set resultsPending here - let searchCalls() set it after guards pass
        // This prevents the guard in searchCalls() from blocking the search
        
        this.searchCalls();
        } finally {
            // Reset guard after search is initiated (but keep it locked until search completes)
            // The guard will be reset when results arrive (in eventHandler)
        }
    }

    ngAfterViewInit(): void {
        this.refreshLoadMoreObserver();
    }

    /** Reset the archive results scroll viewport. */
    scrollResultsToTop(): void {
        const el = this.archiveScroll?.nativeElement;
        if (el) {
            el.scrollTop = 0;
        }
        this.scrollLoadEnabled = false;
    }

    ngOnDestroy(): void {
        this.loadMoreObserver?.disconnect();
        this.loadMoreObserver = undefined;
        this.eventSubscription.unsubscribe();
        
        // Clean up debounce timeout
        if (this.formChangeTimeout) {
            clearTimeout(this.formChangeTimeout);
            this.formChangeTimeout = null;
        }

        this.clearResultsWatchdog();

        // Clear playback list and stop playback mode when search screen is closed
        // This prevents old search results from persisting and auto-playing later
        if (this.livefeedPlayback) {
            this.rdioScannerService.stopPlaybackMode();
        }
    }

    play(id: number): void {
        this.syncPlaybackListToService();
        this.rdioScannerService.loadAndPlay(id);
    }

    refreshFilters(): void {
        if (!this.config) {
            return;
        }

        const selectedGroup = this.getSelectedGroup();
        const selectedSystem = this.getSelectedSystem();
        const selectedTag = this.getSelectedTag();
        const selectedTalkgroups = this.getSelectedTalkgroups();
        const selectedTalkgroup = selectedTalkgroups.length === 1 ? selectedTalkgroups[0] : undefined;
        const selectedTalkgroupLabels = selectedTalkgroups.map((tg) => tg.label);

        this.optionsSystem = this.config.systems
            .filter((system) => {
                const group = selectedGroup === undefined ||
                    system.talkgroups.some((talkgroup) => talkgroup.groups.includes(selectedGroup));
                const tag = selectedTag === undefined ||
                    system.talkgroups.some((talkgroup) => talkgroup.tag === selectedTag);
                return group && tag;
            })
            .map((system) => system.label);

        this.optionsTalkgroup = selectedSystem == undefined
            ? []
            : selectedSystem.talkgroups
                .filter((talkgroup) => {
                    const group = selectedGroup == undefined ||
                        talkgroup.groups.includes(selectedGroup);
                    const tag = selectedTag == undefined ||
                        talkgroup.tag === selectedTag;
                    return group && tag;
                })
                .map((talkgroup) => talkgroup.label);

        this.optionsGroup = Object.keys(this.config.groups)
            .filter((group) => {
                const system: boolean = selectedSystem === undefined ||
                    selectedSystem.talkgroups.some((talkgroup) => talkgroup.groups.includes(group))
                const talkgroup: boolean = selectedTalkgroup === undefined ||
                    selectedTalkgroup.groups.includes(group);
                const tag: boolean = selectedTag === undefined ||
                    (selectedTalkgroup !== undefined && selectedTalkgroup.tag === selectedTag) ||
                    (this.config !== undefined && this.config.systems
                        .flatMap((system) => system.talkgroups)
                        .some((talkgroup) => talkgroup.groups.includes(group) && talkgroup.tag === selectedTag))
                return system && talkgroup && tag;
            })
            .sort((a, b) => a.localeCompare(b))

        this.optionsTag = Object.keys(this.config.tags)
            .filter((tag) => {
                const system: boolean = selectedSystem === undefined ||
                    selectedSystem.talkgroups.some((talkgroup) => talkgroup.tag === tag)
                const talkgroup: boolean = selectedTalkgroup === undefined ||
                    selectedTalkgroup.tag === tag;
                const group: boolean = selectedGroup === undefined ||
                    (selectedTalkgroup !== undefined && selectedTalkgroup.groups.includes(selectedGroup)) ||
                    (this.config !== undefined && this.config.systems
                        .flatMap((system) => system.talkgroups)
                        .some((talkgroup) => talkgroup.tag === tag && talkgroup.groups.includes(selectedGroup)))
                return system && talkgroup && group;
            })
            .sort((a, b) => a.localeCompare(b))

        // Remap selected talkgroup indexes after options rebuild.
        const remappedTalkgroups = selectedTalkgroupLabels
            .map((label) => this.optionsTalkgroup.findIndex((tg) => tg === label))
            .filter((idx) => idx >= 0);

        // Patch form values WITHOUT emitting events to prevent triggering formChangeHandler
        this.form.patchValue({
            group: selectedGroup ? this.optionsGroup.findIndex((group) => group === selectedGroup) : -1,
            system: selectedSystem ? this.optionsSystem.findIndex((system) => system === selectedSystem.label) : -1,
            tag: selectedTag ? this.optionsTag.findIndex((tag) => tag === selectedTag) : -1,
            talkgroups: remappedTalkgroups,
        }, { emitEvent: false });
    }

    /** Push server-loaded rows to the table (one batch appended per scroll-to-bottom). */
    private syncResultsDisplay(): void {
        this.results.next([...this.accumulatedResults]);
        this.ngChangeDetectorRef.detectChanges();
        setTimeout(() => this.refreshLoadMoreObserver(), 0);
    }

    /** Keep the shared service playback list aligned with infinite-scroll accumulation. */
    private syncPlaybackListToService(): void {
        if (!this.playbackList || this.accumulatedResults.length === 0) {
            return;
        }

        this.playbackList.results = [...this.accumulatedResults];
        this.playbackList.count = this.accumulatedResults.length;
        this.playbackList.hasMore = this.hasMoreResults;
        this.playbackList.options = {
            ...this.playbackList.options,
            limit: this.limit,
            offset: 0,
        };
    }

    private refreshLoadMoreObserver(): void {
        this.loadMoreObserver?.disconnect();

        const root = this.archiveScroll?.nativeElement;
        const target = this.loadMoreSentinel?.nativeElement;
        if (!root || !target || typeof IntersectionObserver === 'undefined') {
            return;
        }

        this.loadMoreObserver = new IntersectionObserver(
            (entries) => {
                if (!entries.some((entry) => entry.isIntersecting)) {
                    return;
                }
                if (this.isNearScrollBottom(root)) {
                    this.onLoadMoreRequested();
                }
            },
            { root, rootMargin: '0px', threshold: 0 },
        );
        this.loadMoreObserver.observe(target);
    }

    private isNearScrollBottom(el: HTMLElement): boolean {
        const threshold = 120;
        return el.scrollTop + el.clientHeight >= el.scrollHeight - threshold;
    }

    private onLoadMoreRequested(): void {
        if (!this.scrollLoadEnabled || !this.hasMoreResults || this.resultsPending) {
            return;
        }
        this.loadMoreResults();
    }

    onArchiveScroll(event: Event): void {
        const el = event.target as HTMLElement;
        if (!el) {
            return;
        }
        if (el.scrollTop > 0) {
            this.scrollLoadEnabled = true;
        }
        if (this.isNearScrollBottom(el)) {
            this.onLoadMoreRequested();
        }
    }

    private nextBatchOffset(): number {
        let next = 0;
        for (const loaded of this.loadedOffsets) {
            next = Math.max(next, loaded + this.limit);
        }
        return next;
    }

    private loadMoreResults(): void {
        if (this.livefeedPlayback || this.resultsPending || !this.hasMoreResults || !this.lastSearchOptions) {
            return;
        }

        const nextOffset = this.nextBatchOffset();
        if (this.loadedOffsets.has(nextOffset)) {
            return;
        }

        this.offset = nextOffset;
        this.loadingMore = true;

        const options: RdioScannerSearchOptions = {
            ...this.lastSearchOptions,
            limit: this.limit,
            offset: nextOffset,
        };

        const normalizedOptions: any = {
            system: options.system,
            talkgroup: options.talkgroup,
            talkgroups: options.talkgroups,
            date: options.date ? (options.date instanceof Date ? options.date.toISOString() : options.date) : undefined,
            limit: options.limit,
            offset: options.offset,
            sort: options.sort,
        };
        this.lastRequestId = JSON.stringify(normalizedOptions);
        this.resultsPending = true;
        this.armResultsWatchdog();
        this.rdioScannerService.searchCalls(options);
    }

    resetForm(): void {
        this.form.reset({
            date: null,
            group: -1,
            sort: -1,
            system: -1,
            tag: -1,
            talkgroups: [],
            favorite: -1,
        });

        this.selectedDate = null;
        this.selectedTime = null;

        this.savePrefs();
        this.formChangeHandler();
    }

    setFavorite(value: number): void {
        this.form.get('favorite')?.setValue(value, { emitEvent: false });
        if (value >= 0) {
            this.form.get('talkgroups')?.setValue([], { emitEvent: false });
        }
        this.savePrefs();
        this.formChangeHandler();
    }

    getSelectedFavoriteLabel(): string {
        const index = this.form.value.favorite;
        if (index == null || index < 0) return 'All Calls';
        return this.optionsFavorites[index]?.label || 'All Calls';
    }

    private loadFavorites(): void {
        if (!this.config) {
            this.optionsFavorites = [];
            return;
        }

        const favoriteItems = this.favoritesService.getFavoriteItems();
        this.optionsFavorites = [];

        favoriteItems.forEach(item => {
            if (item.type === 'talkgroup' && item.systemId !== undefined && item.talkgroupId !== undefined) {
                const system = this.config?.systems.find(s => s.id === item.systemId);
                if (system) {
                    const talkgroup = system.talkgroups.find(t => t.id === item.talkgroupId);
                    if (talkgroup) {
                        this.optionsFavorites.push({
                            systemId: item.systemId,
                            talkgroupId: item.talkgroupId,
                            label: `${system.label} - ${talkgroup.label}`
                        });
                    }
                }
            }
        });

        // Sort favorites alphabetically
        this.optionsFavorites.sort((a, b) => a.label.localeCompare(b.label));
    }

    openDatePicker(): void {
        this.datePicker?.open();
    }

    onDateSelected(event: any): void {
        const date = event?.value;
        if (date && date instanceof Date) {
            // Create date at midnight LOCAL time (matching Flutter app behavior)
            // This ensures timezone-correct date filtering
            const localDate = new Date(date.getFullYear(), date.getMonth(), date.getDate(), 0, 0, 0, 0);
            this.selectedDate = localDate;
            const year = localDate.getFullYear();
            const month = String(localDate.getMonth() + 1).padStart(2, '0');
            const day = String(localDate.getDate()).padStart(2, '0');
            const dateString = `${year}-${month}-${day}`;
            this.form.get('date')?.setValue(dateString, { emitEvent: false });
            this.savePrefs();
            this.formChangeHandler();
        } else if (date === null) {
            this.clearDate();
        }
    }

    clearDate(): void {
        this.selectedDate = null;
        this.selectedTime = null; // Time has no meaning without a date.
        this.form.get('date')?.setValue(null, { emitEvent: false });
        this.savePrefs();
        this.formChangeHandler();
    }

    // ───────────────────────── Time-of-day helpers ──────────────────────────

    getHour(): number {
        if (!this.selectedTime) return 0;
        const [h] = this.selectedTime.split(':');
        return parseInt(h, 10) || 0;
    }

    getMinute(): number {
        if (!this.selectedTime) return 0;
        const [, m] = this.selectedTime.split(':');
        return parseInt(m, 10) || 0;
    }

    pad2(n: number): string {
        return String(n).padStart(2, '0');
    }

    getTimeDisplay(): string {
        return `${this.pad2(this.getHour())}:${this.pad2(this.getMinute())}`;
    }

    private setTime(hour: number, minute: number, emit = true): void {
        const h = ((hour % 24) + 24) % 24;
        const m = ((minute % 60) + 60) % 60;
        this.selectedTime = `${this.pad2(h)}:${this.pad2(m)}`;
        if (emit) {
            this.savePrefs();
            this.formChangeHandler();
        }
    }

    bumpHour(delta: number): void {
        this.setTime(this.getHour() + delta, this.getMinute());
    }

    bumpMinute(delta: number): void {
        this.setTime(this.getHour(), this.getMinute() + delta);
    }

    setTimeNow(): void {
        const now = new Date();
        this.setTime(now.getHours(), now.getMinutes());
    }

    clearTime(): void {
        this.selectedTime = null;
        this.savePrefs();
        this.formChangeHandler();
    }

    setSort(value: number): void {
        this.form.get('sort')?.setValue(value, { emitEvent: false });
        this.savePrefs();
        this.formChangeHandler();
    }

    toggleSort(): void {
        const currentSort = this.form.value.sort;
        const newSort = currentSort === -1 ? 1 : -1;
        this.setSort(newSort);
    }

    setSystem(value: number): void {
        this.form.get('system')?.setValue(value, { emitEvent: false });
        this.form.get('talkgroups')?.setValue([], { emitEvent: false });
        this.form.get('favorite')?.setValue(-1, { emitEvent: false });
        this.savePrefs();
        this.formChangeHandler();
    }

    clearTalkgroups(): void {
        this.form.get('talkgroups')?.setValue([], { emitEvent: false });
        this.form.get('favorite')?.setValue(-1, { emitEvent: false });
        this.savePrefs();
        this.formChangeHandler();
    }

    toggleTalkgroup(index: number): void {
        if (index < 0) {
            this.clearTalkgroups();
            return;
        }
        const current: number[] = Array.isArray(this.form.value.talkgroups)
            ? [...this.form.value.talkgroups]
            : [];
        const pos = current.indexOf(index);
        if (pos >= 0) {
            current.splice(pos, 1);
        } else {
            current.push(index);
            current.sort((a, b) => a - b);
        }
        this.form.get('talkgroups')?.setValue(current, { emitEvent: false });
        this.form.get('favorite')?.setValue(-1, { emitEvent: false });
        this.savePrefs();
        this.formChangeHandler();
    }

    isTalkgroupSelected(index: number): boolean {
        const current: number[] = Array.isArray(this.form.value.talkgroups)
            ? this.form.value.talkgroups
            : [];
        return current.includes(index);
    }

    setGroup(value: number): void {
        this.form.get('group')?.setValue(value, { emitEvent: false });
        this.savePrefs();
        this.formChangeHandler();
    }

    setTag(value: number): void {
        this.form.get('tag')?.setValue(value, { emitEvent: false });
        this.savePrefs();
        this.formChangeHandler();
    }

    getSelectedSystemLabel(): string {
        const index = this.form.value.system;
        if (index == null || index < 0) return 'All Systems';
        return this.optionsSystem[index] || 'All Systems';
    }

    getSelectedTalkgroupLabel(): string {
        const selected = this.getSelectedTalkgroups();
        if (selected.length === 0) return 'All Talkgroups';
        if (selected.length === 1) return selected[0].label;
        return `${selected.length} talkgroups`;
    }

    getSelectedGroupLabel(): string {
        const index = this.form.value.group;
        if (index == null || index < 0) return 'All Groups';
        return this.optionsGroup[index] || 'All Groups';
    }

    getSelectedTagLabel(): string {
        const index = this.form.value.tag;
        if (index == null || index < 0) return 'All Tags';
        return this.optionsTag[index] || 'All Tags';
    }

    searchCalls(): void {
        if (this.livefeedPlayback) {
            return;
        }

        this.loadingMore = false;
        this.offset = 0;

        const options: RdioScannerSearchOptions = {
            limit: this.limit,
            offset: 0,
            sort: this.form.value.sort,
        };

        if (this.selectedDate) {
            const dt = new Date(
                this.selectedDate.getFullYear(),
                this.selectedDate.getMonth(),
                this.selectedDate.getDate(),
                this.selectedTime ? this.getHour() : 0,
                this.selectedTime ? this.getMinute() : 0,
                0, 0,
            );
            options.date = dt.toISOString() as any;
        } else if (typeof this.form.value.date === 'string') {
            const dateObj = new Date(this.form.value.date);
            if (!isNaN(dateObj.getTime())) {
                const localDate = new Date(dateObj.getFullYear(), dateObj.getMonth(), dateObj.getDate(), 0, 0, 0, 0);
                options.date = localDate.toISOString() as any;
            }
        }

        if ((this.form.value.group ?? -1) >= 0) {
            const group = this.getSelectedGroup();
            if (group) {
                options.group = group;
            }
        }

        if ((this.form.value.system ?? -1) >= 0) {
            const system = this.getSelectedSystem();
            if (system) {
                options.system = system.id;
            }
        }

        if ((this.form.value.tag ?? -1) >= 0) {
            const tag = this.getSelectedTag();
            if (tag) {
                options.tag = tag;
            }
        }

        const selectedTalkgroups = this.getSelectedTalkgroups();
        if (selectedTalkgroups.length === 1) {
            options.talkgroup = selectedTalkgroups[0].id;
        } else if (selectedTalkgroups.length > 1) {
            options.talkgroups = selectedTalkgroups.map((tg) => tg.id);
        }

        if ((this.form.value.favorite ?? -1) >= 0) {
            const favorite = this.optionsFavorites[this.form.value.favorite];
            if (favorite) {
                options.system = favorite.systemId;
                options.talkgroup = favorite.talkgroupId;
                delete options.talkgroups;
            }
        }

        const currentFilters = {
            date: options.date,
            group: options.group,
            system: options.system,
            tag: options.tag,
            talkgroup: options.talkgroup,
            talkgroups: options.talkgroups,
            sort: options.sort,
        };
        const lastFilters = this.lastSearchOptions ? {
            date: this.lastSearchOptions.date,
            group: this.lastSearchOptions.group,
            system: this.lastSearchOptions.system,
            tag: this.lastSearchOptions.tag,
            talkgroup: this.lastSearchOptions.talkgroup,
            talkgroups: this.lastSearchOptions.talkgroups,
            sort: this.lastSearchOptions.sort,
        } : null;
        const optionsChanged = !lastFilters || JSON.stringify(currentFilters) !== JSON.stringify(lastFilters);

        if (optionsChanged) {
            this.accumulatedResults = [];
            this.loadedOffsets.clear();
            this.hasMoreResults = false;
            this.scrollLoadEnabled = false;
            this.offset = 0;
            options.offset = 0;
        }

        this.lastSearchOptions = { ...options };

        if (!optionsChanged && this.loadedOffsets.has(this.offset)) {
            this.syncResultsDisplay();
            return;
        }

        if (this.resultsPending) {
            return;
        }

        const normalizedOptions: any = {
            system: options.system,
            talkgroup: options.talkgroup,
            talkgroups: options.talkgroups,
            date: options.date ? (options.date instanceof Date ? options.date.toISOString() : options.date) : undefined,
            limit: options.limit,
            offset: options.offset,
            sort: options.sort,
        };
        const requestId = JSON.stringify(normalizedOptions);

        if (this.lastRequestId === requestId && this.offset === 0) {
            return;
        }

        this.lastRequestId = requestId;
        this.resultsPending = true;
        this.armResultsWatchdog();
        this.form.disable();

        this.rdioScannerService.searchCalls(options);
    }

    /**
     * Start (or restart) the resultsPending watchdog. Call this whenever
     * `resultsPending` flips to true. If the playbackList event never
     * arrives within `resultsPendingWatchdogMs`, the UI is force-recovered
     * so the user can pick a different date / clear the filter / retry
     * without reloading the whole page.
     */
    private armResultsWatchdog(): void {
        this.clearResultsWatchdog();
        this.resultsPendingWatchdog = setTimeout(() => {
            this.resultsPendingWatchdog = null;
            if (!this.resultsPending) return;
            console.warn('[rdio-scanner-search] no WS reply for archive search within '
                + `${this.resultsPendingWatchdogMs}ms — clearing stuck loading state.`);
            this.resultsPending = false;
            this.loadingMore = false;
            this.isExecutingFormChange = false;
            this.lastRequestId = null;
            try { this.form.enable(); } catch { /* form may already be enabled */ }
            this.ngChangeDetectorRef.detectChanges();
        }, this.resultsPendingWatchdogMs);
    }

    private clearResultsWatchdog(): void {
        if (this.resultsPendingWatchdog) {
            clearTimeout(this.resultsPendingWatchdog);
            this.resultsPendingWatchdog = null;
        }
    }

    stop(): void {
        if (this.livefeedPlayback) {
            // Stop the current call but keep the archive result list so the
            // next Play can auto-advance through search results again.
            this.rdioScannerService.stopPlaybackMode({ clearList: false });
        } else {
            this.rdioScannerService.stop();
        }
    }

    private eventHandler(event: RdioScannerEvent): void {
        if ('call' in event) {
            this.call = event.call;

            if (this.callPending) {
                const found = this.accumulatedResults.some((call) => call?.id === this.callPending);
                if (!found && this.hasMoreResults && !this.resultsPending) {
                    this.loadMoreResults();
                } else if (found) {
                    this.callPending = undefined;
                }
            }
        }

        if ('config' in event) {
            this.config = event.config;

            this.callPending = undefined;

            this.optionsGroup = Object.keys(this.config?.groups || []).sort((a, b) => a.localeCompare(b));
            this.optionsSystem = (this.config?.systems || []).map((system) => system.label);
            this.optionsTag = Object.keys(this.config?.tags || []).sort((a, b) => a.localeCompare(b));
            
            this.loadFavorites();

            this.time12h = this.config?.time12hFormat || false;

            // Issue #185: restore saved playback filters now that config is available.
            this.applyPendingPrefs();

            // Auto-select system if only one exists (UX improvement for single-system setups)
            if (this.optionsSystem.length === 1 && this.form.value.system === -1) {
                this.form.patchValue({ system: 0 }, { emitEvent: false });
                this.refreshFilters(); // Populate talkgroups for the selected system
            }
        }

        if ('livefeedMode' in event) {
            this.livefeedOnline = event.livefeedMode === RdioScannerLivefeedMode.Online;

            this.livefeedPlayback = event.livefeedMode === RdioScannerLivefeedMode.Playback;
        }

        if ('playbackList' in event) {
            this.playbackList = event.playbackList;

            // Accumulate results from this batch
            if (this.playbackList && this.playbackList.results) {
                // Get the offset from the options (handles pre-fetched batches)
                const batchOffset = this.playbackList.options?.offset ?? 0;

                if (batchOffset === 0) {
                    this.accumulatedResults = [];
                    this.loadedOffsets.clear();
                }

                this.loadedOffsets.add(batchOffset);
                this.hasMoreResults = !!this.playbackList.hasMore;
                for (let i = 0; i < this.playbackList.results.length; i++) {
                    const insertIndex = batchOffset + i;
                    if (insertIndex >= this.accumulatedResults.length) {
                        this.accumulatedResults.push(this.playbackList.results[i]);
                    } else {
                        this.accumulatedResults[insertIndex] = this.playbackList.results[i];
                    }
                }

                this.syncPlaybackListToService();
            }

            this.resultsPending = false;
            this.loadingMore = false;
            this.clearResultsWatchdog();
            this.form.enable();
            this.isExecutingFormChange = false;

            this.syncResultsDisplay();

            if (this.callPending) {
                const found = this.accumulatedResults.some((call) => call?.id === this.callPending);
                if (!found && this.hasMoreResults) {
                    this.loadMoreResults();
                } else if (found) {
                    this.callPending = undefined;
                }
            }
        }

        if ('playbackPending' in event) {
            this.callPending = event.playbackPending;
        }

        if ('pause' in event) {
            this.paused = event.pause || false;
        }

        this.ngChangeDetectorRef.detectChanges();
    }

    private getSelectedGroup(): string | undefined {
        const groupIndex = this.form.value.group;
        return groupIndex != null && groupIndex >= 0 ? this.optionsGroup[groupIndex] : undefined;
    }

    private getSelectedSystem(): RdioScannerSystem | undefined {
        const systemIndex = this.form.value.system;
        if (systemIndex == null || systemIndex < 0) return undefined;
        return this.config?.systems.find((system) => system.label === this.optionsSystem[systemIndex]);
    }

    private getSelectedTag(): string | undefined {
        const tagIndex = this.form.value.tag;
        return tagIndex != null && tagIndex >= 0 ? this.optionsTag[tagIndex] : undefined;
    }

    getSelectedTalkgroups(): RdioScannerTalkgroup[] {
        const system = this.getSelectedSystem();
        if (!system) return [];
        const indexes: number[] = Array.isArray(this.form.value.talkgroups)
            ? this.form.value.talkgroups
            : [];
        const out: RdioScannerTalkgroup[] = [];
        for (const talkgroupIndex of indexes) {
            if (talkgroupIndex == null || talkgroupIndex < 0) continue;
            const label = this.optionsTalkgroup[talkgroupIndex];
            if (!label) continue;
            const talkgroup = system.talkgroups.find((tg) => tg.label === label);
            if (talkgroup) {
                out.push(talkgroup);
            }
        }
        return out;
    }

    private getSelectedTalkgroup(): RdioScannerTalkgroup | undefined {
        const selected = this.getSelectedTalkgroups();
        return selected.length === 1 ? selected[0] : undefined;
    }

    /** Format playback row duration; em dash when missing/zero. */
    formatCallDuration(seconds?: number | null): string {
        if (seconds == null || !Number.isFinite(seconds) || seconds <= 0) {
            return '—';
        }
        const total = Math.round(seconds);
        const hours = Math.floor(total / 3600);
        const minutes = Math.floor((total % 3600) / 60);
        const secs = total % 60;
        if (hours > 0) {
            return `${hours}:${String(minutes).padStart(2, '0')}:${String(secs).padStart(2, '0')}`;
        }
        return `${minutes}:${String(secs).padStart(2, '0')}`;
    }

    // ── Issue #185: persistence ────────────────────────────────────────────────

    /** Read saved playback prefs from localStorage; returns null on miss/parse error. */
    private loadPrefs(): PlaybackPrefs | null {
        try {
            if (typeof localStorage === 'undefined') return null;
            const raw = localStorage.getItem(PLAYBACK_PREFS_STORAGE_KEY);
            if (!raw) return null;
            const parsed = JSON.parse(raw);
            if (parsed && typeof parsed === 'object') return parsed as PlaybackPrefs;
        } catch { /* corrupt prefs — ignore and start fresh */ }
        return null;
    }

    /** Persist current filter + pagination state. Cheap and safe to call on every change. */
    private savePrefs(): void {
        try {
            if (typeof localStorage === 'undefined') return;
            const system = this.getSelectedSystem();
            const talkgroups = this.getSelectedTalkgroups();
            const group = this.getSelectedGroup();
            const tag = this.getSelectedTag();
            const favIdx = this.form.value.favorite ?? -1;
            const fav = favIdx >= 0 ? this.optionsFavorites[favIdx] : undefined;

            const prefs: PlaybackPrefs = {
                systemLabel: system?.label,
                talkgroupLabel: talkgroups.length === 1 ? talkgroups[0].label : undefined,
                talkgroupLabels: talkgroups.map((tg) => tg.label),
                groupLabel: group,
                tagLabel: tag,
                favoriteKey: fav ? `${fav.systemId}:${fav.talkgroupId}` : undefined,
                date: this.selectedDate ? this.selectedDate.toISOString() : undefined,
                time: this.selectedTime ?? undefined,
                sort: this.form.value.sort ?? -1,
            };
            localStorage.setItem(PLAYBACK_PREFS_STORAGE_KEY, JSON.stringify(prefs));
        } catch { /* quota or disabled storage — silently skip */ }
    }

    /**
     * Apply previously-saved prefs once the config has arrived. Stored values are
     * looked up by label (system/talkgroup/group/tag) and key
     * (`${systemId}:${talkgroupId}` for favorites) so we recover gracefully from
     * config reorderings or talkgroups that no longer exist.
     */
    private applyPendingPrefs(): void {
        if (this.prefsApplied || !this.pendingPrefs || !this.config) return;
        const prefs = this.pendingPrefs;
        this.prefsApplied = true;

        const patch: Record<string, number | string | null> = {};

        if (prefs.systemLabel) {
            const idx = this.optionsSystem.findIndex((label) => label === prefs.systemLabel);
            if (idx >= 0) patch['system'] = idx;
        }
        if (prefs.groupLabel) {
            const idx = this.optionsGroup.findIndex((label) => label === prefs.groupLabel);
            if (idx >= 0) patch['group'] = idx;
        }
        if (prefs.tagLabel) {
            const idx = this.optionsTag.findIndex((label) => label === prefs.tagLabel);
            if (idx >= 0) patch['tag'] = idx;
        }

        if (Object.keys(patch).length > 0) {
            this.form.patchValue(patch, { emitEvent: false });
            // System patched in: rebuild dependent option arrays before patching talkgroup.
            this.refreshFilters();
        }

        const savedTalkgroupLabels = (prefs.talkgroupLabels && prefs.talkgroupLabels.length > 0)
            ? prefs.talkgroupLabels
            : (prefs.talkgroupLabel ? [prefs.talkgroupLabel] : []);
        if (savedTalkgroupLabels.length > 0) {
            const indexes = savedTalkgroupLabels
                .map((label) => this.optionsTalkgroup.findIndex((tg) => tg === label))
                .filter((idx) => idx >= 0);
            this.form.get('talkgroups')?.setValue(indexes, { emitEvent: false });
        }

        if (prefs.favoriteKey) {
            const idx = this.optionsFavorites.findIndex((f) => `${f.systemId}:${f.talkgroupId}` === prefs.favoriteKey);
            if (idx >= 0) this.form.get('favorite')?.setValue(idx, { emitEvent: false });
        }

        // After config-dependent restoration is in place, kick a search so the
        // table populates without requiring the user to click anything.
        const restoredTalkgroups: number[] = Array.isArray(this.form.value.talkgroups)
            ? this.form.value.talkgroups
            : [];
        if (
            patch['system'] !== undefined ||
            restoredTalkgroups.length > 0 ||
            this.form.value.favorite >= 0 ||
            this.selectedDate
        ) {
            this.searchCalls();
        }
    }

    /** Same tag-tinted row background as Current call history. */
    getTransmissionHistoryTagColor(call: RdioScannerCall | undefined | null): string {
        if (!call) {
            return 'transparent';
        }
        if (call.tagData?.led) {
            return this.tagColorService.getTagColor(call.tagData.led);
        }
        if (call.talkgroupData?.tag) {
            return this.tagColorService.getTagColor(call.talkgroupData.tag);
        }
        return 'transparent';
    }

    getTransmissionHistoryBackgroundColor(call: RdioScannerCall | undefined | null): string {
        const color = this.getTransmissionHistoryTagColor(call);
        if (color === 'transparent') {
            return 'transparent';
        }
        const hex = color.replace('#', '');
        const full =
            hex.length === 3
                ? hex
                      .split('')
                      .map((c) => c + c)
                      .join('')
                : hex;
        if (full.length !== 6) {
            return 'transparent';
        }
        const r = parseInt(full.slice(0, 2), 16);
        const g = parseInt(full.slice(2, 4), 16);
        const b = parseInt(full.slice(4, 6), 16);
        return `rgba(${r}, ${g}, ${b}, 0.2)`;
    }

    displayTgidForCall(call: RdioScannerCall | undefined | null): string {
        if (!call) return '—';
        if (this.isAfsSystem(call)) {
            return this.formatAfs(Number(call.talkgroup ?? 0));
        }
        return String(call.talkgroup ?? '0');
    }

    private formatAfs(n: number): string {
        return `${((n >> 7) & 15).toString().padStart(2, '0')}-${((n >> 3) & 15).toString().padStart(2, '0')}${
            n & 7
        }`;
    }

    private isAfsSystem(call: RdioScannerCall): boolean {
        return call.systemData?.type === 'provoice' || call.talkgroupData?.type === 'provoice';
    }

    private resolveUnitLabelForSrc(call: RdioScannerCall, src: number): string {
        return resolveUnitLabelForSrc(call.systemData?.units, src);
    }

    displayUnitForCall(call: RdioScannerCall | undefined | null): string {
        if (!call) return '—';
        if (Array.isArray(call.sources) && call.sources.length) {
            const ordered = [...call.sources].sort((a, b) => (a.pos || 0) - (b.pos || 0));
            for (const s of ordered) {
                if (typeof s.tag === 'string' && s.tag.length > 0) {
                    return s.tag;
                }
            }
            const first = ordered[0];
            if (typeof first?.src === 'number') {
                return this.resolveUnitLabelForSrc(call, first.src);
            }
        }
        if (typeof call.source === 'number') {
            return this.resolveUnitLabelForSrc(call, call.source);
        }
        return '—';
    }
}
