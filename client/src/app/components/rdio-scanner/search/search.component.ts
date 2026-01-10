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

import { ChangeDetectorRef, Component, OnDestroy, ViewChild } from '@angular/core';
import { FormBuilder } from '@angular/forms';
import { MatDatepicker } from '@angular/material/datepicker';
import { MatPaginator } from '@angular/material/paginator';
import { BehaviorSubject } from 'rxjs';
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

@Component({
    selector: 'rdio-scanner-search',
    styleUrls: ['./search.component.scss'],
    templateUrl: './search.component.html',
})
export class RdioScannerSearchComponent implements OnDestroy {
    call: RdioScannerCall | undefined;
    callPending: number | undefined;

    form: any;

    constructor(
        private rdioScannerService: RdioScannerService,
        private ngChangeDetectorRef: ChangeDetectorRef,
        private ngFormBuilder: FormBuilder,
        private favoritesService: FavoritesService,
    ) {
        this.form = this.ngFormBuilder.group({
            date: [null],
            group: [-1],
            sort: [-1],
            system: [-1],
            tag: [-1],
            talkgroup: [-1],
            favorite: [-1],
        });
        
        // Initialize selectedDate from form if it exists
        if (this.form.value.date) {
            const dateStr = this.form.value.date;
            if (typeof dateStr === 'string') {
                const dateObj = new Date(dateStr);
                if (!isNaN(dateObj.getTime())) {
                    this.selectedDate = dateObj;
                }
            }
        }
        
        this.eventSubscription = this.rdioScannerService.event.subscribe((event: RdioScannerEvent) => this.eventHandler(event));
    }

    livefeedOnline = false;
    livefeedPlayback = false;

    playbackList: RdioScannerPlaybackList | undefined;
    paginatorCount = 0; // Preserve count even when playbackList is cleared during playback

    optionsGroup: string[] = [];
    optionsSystem: string[] = [];
    optionsTag: string[] = [];
    optionsTalkgroup: string[] = [];
    optionsFavorites: Array<{systemId: number, talkgroupId: number, label: string}> = [];

    paused = false;

    results = new BehaviorSubject(new Array<RdioScannerCall | null>(10));
    resultsPending = false;

    time12h = false;

    private config: RdioScannerConfig | undefined;

    private eventSubscription: any;

    private limit = 200;

    private offset = 0;
    
    // Track accumulated results from all loaded batches
    private accumulatedResults: RdioScannerCall[] = [];
    private loadedOffsets: Set<number> = new Set();
    hasMoreResults = false;
    private lastSearchOptions: RdioScannerSearchOptions | null = null;
    private isRefreshing = false; // Guard flag to prevent recursive calls
    private formChangeTimeout: any = null; // Debounce timer for form changes
    private isExecutingFormChange = false; // Guard to prevent multiple simultaneous form change executions
    private lastRequestId: string | null = null; // Track last request to prevent duplicates

    @ViewChild(MatPaginator, { read: MatPaginator }) private paginator: MatPaginator | undefined;
    @ViewChild('datePicker') private datePicker: MatDatepicker<Date> | undefined;
    
    selectedDate: Date | null = null;

    download(id: number): void {
        this.rdioScannerService.loadAndDownload(id);
    }

    formChangeHandler(): void {
        if (this.livefeedPlayback) {
            this.rdioScannerService.stopPlaybackMode();
        }

        // Prevent multiple rapid calls - check if search is pending or already executing
        if (this.resultsPending || this.isExecutingFormChange) {
            return;
        }

        // Debounce form changes to prevent repeated requests (especially for date input)
        // Clear any existing timeout to reset the debounce timer
        if (this.formChangeTimeout) {
            clearTimeout(this.formChangeTimeout);
            this.formChangeTimeout = null;
        }

        // Set new timeout - wait 1000ms before executing (longer debounce for date input to prevent rapid-fire requests)
        this.formChangeTimeout = setTimeout(() => {
            // Double-check guard before executing in case state changed during debounce
            if (!this.isExecutingFormChange && !this.resultsPending) {
                this._executeFormChange();
            }
            this.formChangeTimeout = null;
        }, 1000);
    }

    private _executeFormChange(): void {
        // Prevent multiple simultaneous executions - CRITICAL for date input
        if (this.isExecutingFormChange || this.resultsPending) {
            return;
        }
        
        this.isExecutingFormChange = true;
        
        try {

        this.paginator?.firstPage();

        // Reset accumulation for new search (matching Flutter app behavior)
        this.accumulatedResults = [];
        this.loadedOffsets.clear();
        this.hasMoreResults = false;
        this.lastSearchOptions = null;
        this.lastRequestId = null; // Reset request ID for new search
        this.offset = 0;
        
        // Clear display immediately when filters change
        this.results.next(new Array<RdioScannerCall | null>(10).fill(null));
        this.playbackList = undefined;
        this.paginatorCount = 0;

        this.refreshFilters();

        // Don't set resultsPending here - let searchCalls() set it after guards pass
        // This prevents the guard in searchCalls() from blocking the search
        
        this.searchCalls();
        } finally {
            // Reset guard after search is initiated (but keep it locked until search completes)
            // The guard will be reset when results arrive (in eventHandler)
        }
    }

    ngOnDestroy(): void {
        this.eventSubscription.unsubscribe();
        
        // Clean up debounce timeout
        if (this.formChangeTimeout) {
            clearTimeout(this.formChangeTimeout);
            this.formChangeTimeout = null;
        }
        
        // Clear playback list and stop playback mode when search screen is closed
        // This prevents old search results from persisting and auto-playing later
        if (this.livefeedPlayback) {
            this.rdioScannerService.stopPlaybackMode();
        }
    }

    play(id: number): void {
        this.rdioScannerService.loadAndPlay(id);
    }

    refreshFilters(): void {
        if (!this.config) {
            return;
        }

        const selectedGroup = this.getSelectedGroup();
        const selectedSystem = this.getSelectedSystem();
        const selectedTag = this.getSelectedTag();
        const selectedTalkgroup = this.getSelectedTalkgroup();

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

        // Patch form values WITHOUT emitting events to prevent triggering formChangeHandler
        this.form.patchValue({
            group: selectedGroup ? this.optionsGroup.findIndex((group) => group === selectedGroup) : -1,
            system: selectedSystem ? this.optionsSystem.findIndex((system) => system === selectedSystem.label) : -1,
            tag: selectedTag ? this.optionsTag.findIndex((tag) => tag === selectedTag) : -1,
            talkgroup: selectedTalkgroup ? this.optionsTalkgroup.findIndex((talkgroup) => talkgroup === selectedTalkgroup.label) : -1,
        }, { emitEvent: false });
    }

    refreshResults(): void {
        // Don't block when called from eventHandler (results have already arrived)
        // Only prevent recursive pagination-triggered refreshes
        
        // If paginator isn't ready but we have results, update display directly
        if (!this.paginator) {
            if (this.accumulatedResults.length > 0) {
                const pageSize = 10; // Default page size
                const from = 0;
                const to = pageSize - 1;
                const calls: Array<RdioScannerCall | null> = this.accumulatedResults.slice(from, Math.min(to + 1, this.accumulatedResults.length));
                
                while (calls.length < this.results.value.length) {
                    calls.push(null);
                }
                
                this.results.next(calls);
                console.log(`Display updated (no paginator): showing ${calls.filter(c => c !== null).length} calls`);
            }
            return;
        }

        const pageIndex = this.paginator.pageIndex;
        const pageSize = this.paginator.pageSize;
        const from = pageIndex * pageSize;
        const to = from + pageSize - 1;

        // Calculate which batch (offset) is needed for the current page
        const requiredOffset = Math.floor(from / this.limit) * this.limit;
        
        // Check if we need to fetch more data for the current page
        // But FIRST ensure we display what we have before trying to fetch more
        if (!this.resultsPending && this.accumulatedResults.length === 0) {
            // Only fetch if we have no results at all
            const needsCurrentBatch = !this.loadedOffsets.has(requiredOffset);
            
            if (needsCurrentBatch) {
                // Need to fetch this batch
                this.offset = requiredOffset;
                this.searchCalls();
                return;
            }
        }
        
        // FIRST: Check if we need to fetch data for the current page
        // This must happen before displaying to ensure we have the data
        if (!this.resultsPending) {
            // Check if we have data for the current page
            // Only fetch if we DON'T have data for the page
            const hasDataForPage = from < this.accumulatedResults.length;
            
            if (!hasDataForPage) {
                // We don't have data for this page - check if batch is loaded
                const batchLoaded = this.loadedOffsets.has(requiredOffset);
                
                if (!batchLoaded) {
                    // Batch isn't loaded yet - fetch it
                    console.log(`Fetching batch at offset ${requiredOffset} for page ${pageIndex + 1} (from=${from}, accumulated=${this.accumulatedResults.length})`);
                    this.offset = requiredOffset;
                    this.searchCalls();
                    return; // Don't try to display yet, wait for results
                }
                // If batch is marked as loaded but we don't have data, something went wrong
                // This shouldn't happen, but don't fetch again if batch is marked as loaded
            }
            // If we have data for the page, don't fetch - just display it below
        }
        
        // SECOND: Display results if we have data for the current page
        if (this.accumulatedResults.length > 0 && from < this.accumulatedResults.length) {
            // We have data for this page - display it immediately
            const calls: Array<RdioScannerCall | null> = this.accumulatedResults.slice(from, Math.min(to + 1, this.accumulatedResults.length));

            // Ensure we always have the expected number of rows for the table
            while (calls.length < this.results.value.length) {
                calls.push(null);
            }

            this.results.next(calls);
            this.ngChangeDetectorRef.detectChanges();
            console.log(`Display updated (navigating to page ${pageIndex + 1}): showing ${calls.filter(c => c !== null).length} calls from index ${from} to ${Math.min(to, this.accumulatedResults.length - 1)}, total accumulated: ${this.accumulatedResults.length}`);
        }
        
        // THIRD: Pre-fetch next batch when approaching end of current batch
        if (!this.resultsPending && this.hasMoreResults) {
            const pagesInBatch = Math.floor(this.limit / pageSize); // 20 pages per batch (200 calls / 10 per page)
            const currentBatchNumber = Math.floor(pageIndex / pagesInBatch);
            const currentBatchStartPage = currentBatchNumber * pagesInBatch;
            const currentBatchEndPage = currentBatchStartPage + pagesInBatch - 1;
            const pageWithinBatch = pageIndex - currentBatchStartPage; // 0-19 for pages in current batch
            
            // When reaching the last 2 pages of current batch, pre-fetch next batch
            // This works for ANY batch: 
            // - First batch: pages 18-19 (triggers on pages 19-20, 1-based)
            // - Second batch: pages 38-39 (triggers on pages 39-40, 1-based) 
            // - Third batch: pages 58-59 (triggers on pages 59-60, 1-based)
            // pageWithinBatch is 0-based: 0-19 within each batch
            if (pageWithinBatch >= pagesInBatch - 2) {
                const nextBatchOffset = requiredOffset + this.limit;
                if (!this.loadedOffsets.has(nextBatchOffset)) {
                    // Pre-fetch the next batch in background without blocking
                    console.log(`Pre-fetching next batch at offset ${nextBatchOffset} (batch ${currentBatchNumber + 1}, page ${pageIndex + 1} of ${currentBatchEndPage + 1}, pageWithinBatch=${pageWithinBatch} of ${pagesInBatch - 1})`);
                    const nextOptions: RdioScannerSearchOptions = { ...this.lastSearchOptions! };
                    nextOptions.offset = nextBatchOffset;
                    // Use service directly without updating component state to avoid recursion
                    this.rdioScannerService.searchCalls(nextOptions);
                }
            }
        }

        // Display results from accumulated results (fallback if above didn't run)
        if (this.accumulatedResults.length > 0) {
            // Always display results if we have any
            const calls: Array<RdioScannerCall | null> = this.accumulatedResults.slice(from, Math.min(to + 1, this.accumulatedResults.length));

            // Ensure we always have the expected number of rows for the table
            while (calls.length < this.results.value.length) {
                calls.push(null);
            }

            this.results.next(calls);
            this.ngChangeDetectorRef.detectChanges();
        } else if (this.accumulatedResults.length === 0 && !this.resultsPending && !this.callPending) {
            // No results yet, trigger initial search
            this.offset = 0;
            this.searchCalls();
        }
    }

    resetForm(): void {
        this.form.reset({
            date: null,
            group: -1,
            sort: -1,
            system: -1,
            tag: -1,
            talkgroup: -1,
            favorite: -1,
        });

        this.selectedDate = null;
        this.paginator?.firstPage();

        this.formChangeHandler();
    }

    setFavorite(value: number): void {
        this.form.get('favorite')?.setValue(value, { emitEvent: false });
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
            this.formChangeHandler();
        } else if (date === null) {
            this.clearDate();
        }
    }

    clearDate(): void {
        this.selectedDate = null;
        this.form.get('date')?.setValue(null, { emitEvent: false });
        this.formChangeHandler();
    }

    setSort(value: number): void {
        this.form.get('sort')?.setValue(value, { emitEvent: false });
        this.formChangeHandler();
    }

    toggleSort(): void {
        const currentSort = this.form.value.sort;
        const newSort = currentSort === -1 ? 1 : -1;
        this.setSort(newSort);
    }

    setSystem(value: number): void {
        this.form.get('system')?.setValue(value, { emitEvent: false });
        this.formChangeHandler();
    }

    setTalkgroup(value: number): void {
        this.form.get('talkgroup')?.setValue(value, { emitEvent: false });
        this.formChangeHandler();
    }

    setGroup(value: number): void {
        this.form.get('group')?.setValue(value, { emitEvent: false });
        this.formChangeHandler();
    }

    setTag(value: number): void {
        this.form.get('tag')?.setValue(value, { emitEvent: false });
        this.formChangeHandler();
    }

    getSelectedSystemLabel(): string {
        const index = this.form.value.system;
        if (index == null || index < 0) return 'All Systems';
        return this.optionsSystem[index] || 'All Systems';
    }

    getSelectedTalkgroupLabel(): string {
        const index = this.form.value.talkgroup;
        if (index == null || index < 0) return 'All Talkgroups';
        return this.optionsTalkgroup[index] || 'All Talkgroups';
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

        const pageIndex = this.paginator?.pageIndex || 0;
        const pageSize = this.paginator?.pageSize || 10;

        // Calculate offset based on current page (matching Flutter app logic)
        this.offset = Math.floor((pageIndex * pageSize) / this.limit) * this.limit;

        const options: RdioScannerSearchOptions = {
            limit: this.limit,
            offset: this.offset,
            sort: this.form.value.sort,
        };

        if (this.selectedDate) {
            // Convert Date object to ISO string for backend (RFC3339 format)
            // Date is already in local timezone (midnight local time), .toISOString() converts to UTC
            // This matches Flutter app behavior: local time → UTC conversion
            // Example: Jan 9 midnight EST becomes "2025-01-09T05:00:00.000Z"
            const isoString = this.selectedDate.toISOString();
            options.date = isoString as any;
        } else if (typeof this.form.value.date === 'string') {
            // Fallback: Convert datetime-local string to ISO string for backend (RFC3339 format)
            const dateObj = new Date(this.form.value.date);
            if (!isNaN(dateObj.getTime())) {
                // Ensure we create at midnight local time before converting to UTC
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

        if ((this.form.value.talkgroup ?? -1) >= 0) {
            const talkgroup = this.getSelectedTalkgroup();

            if (talkgroup) {
                options.talkgroup = talkgroup.id;
            }
        }

        // If a favorite is selected, override with that specific talkgroup
        if ((this.form.value.favorite ?? -1) >= 0) {
            const favorite = this.optionsFavorites[this.form.value.favorite];
            if (favorite) {
                options.system = favorite.systemId;
                options.talkgroup = favorite.talkgroupId;
            }
        }

        // Check if search options have changed (reset accumulation if so)
        // Compare only filter-relevant fields, NOT offset or limit (those are for pagination)
        // If lastSearchOptions is null, treat it as changed (matching Flutter app behavior)
        const currentFilters = {
            date: options.date,
            group: options.group,
            system: options.system,
            tag: options.tag,
            talkgroup: options.talkgroup,
            sort: options.sort
        };
        const lastFilters = this.lastSearchOptions ? {
            date: this.lastSearchOptions.date,
            group: this.lastSearchOptions.group,
            system: this.lastSearchOptions.system,
            tag: this.lastSearchOptions.tag,
            talkgroup: this.lastSearchOptions.talkgroup,
            sort: this.lastSearchOptions.sort
        } : null;
        const optionsChanged = !lastFilters || JSON.stringify(currentFilters) !== JSON.stringify(lastFilters);
        
        if (optionsChanged) {
            this.accumulatedResults = [];
            this.loadedOffsets.clear();
            this.hasMoreResults = false;
            // When options change (like system filter), always reset offset to 0
            this.offset = 0;
            options.offset = 0;
            // Reset paginator to first page if not already there
            if (pageIndex !== 0) {
                this.paginator?.firstPage();
                return; // Will trigger again after pagination reset
            }
        }
        this.lastSearchOptions = {...options}; // Store a copy

        // If this offset is already loaded and options haven't changed, don't fetch again - just update display
        if (!optionsChanged && this.loadedOffsets.has(this.offset)) {
            // Just refresh the display without fetching
            if (this.accumulatedResults.length > 0) {
                const from = pageIndex * pageSize;
                const to = from + pageSize - 1;
                
                if (from < this.accumulatedResults.length) {
                    const calls: Array<RdioScannerCall | null> = this.accumulatedResults.slice(from, Math.min(to + 1, this.accumulatedResults.length));

                    while (calls.length < this.results.value.length) {
                        calls.push(null);
                    }

                    this.results.next(calls);
                }
            }
            return;
        }

        // Prevent multiple simultaneous searches
        if (this.resultsPending) {
            return;
        }

        // Create a normalized request ID to prevent duplicate requests
        // Normalize date to ISO string for consistent comparison
        const normalizedOptions: any = {
            system: options.system,
            talkgroup: options.talkgroup,
            date: options.date ? (options.date instanceof Date ? options.date.toISOString() : options.date) : undefined,
            limit: options.limit,
            offset: options.offset,
            sort: options.sort
        };
        const requestId = JSON.stringify(normalizedOptions);
        
        // If this is the same request as the last one, skip it (unless we're on a different page)
        if (this.lastRequestId === requestId && this.offset === 0) {
            return;
        }
        
        this.lastRequestId = requestId;
        this.resultsPending = true;

        this.form.disable();

        this.rdioScannerService.searchCalls(options);
    }

    stop(): void {
        if (this.livefeedPlayback) {
            this.rdioScannerService.stopPlaybackMode();

        } else {
            this.rdioScannerService.stop();
        }
    }

    private eventHandler(event: RdioScannerEvent): void {
        if ('call' in event) {
            this.call = event.call;

            if (this.callPending) {
                const index = this.results.value.findIndex((call) => call?.id === this.callPending);

                if (index === -1) {
                    if (this.form.value.sort === -1) {
                        this.paginator?.previousPage();

                    } else {
                        this.paginator?.nextPage();
                    }
                }

                this.callPending = undefined;
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
                
                // Mark this offset as loaded
                this.loadedOffsets.add(batchOffset);
                
                // Update hasMore flag
                this.hasMoreResults = this.playbackList.hasMore || false;
                
                // If this is a new search (offset 0), always reset accumulated results
                // This ensures we start fresh for each new search
                if (batchOffset === 0) {
                    this.accumulatedResults = [];
                    this.loadedOffsets.clear();
                }
                
                // Append new results to accumulated results
                // For offset 0 (new search), start from index 0
                // For subsequent batches, start from the offset index
                for (let i = 0; i < this.playbackList.results.length; i++) {
                    const insertIndex = batchOffset + i;
                    if (insertIndex >= this.accumulatedResults.length) {
                        this.accumulatedResults.push(this.playbackList.results[i]);
                    } else {
                        // Replace if already exists (shouldn't happen, but be safe)
                        this.accumulatedResults[insertIndex] = this.playbackList.results[i];
                    }
                }
                
                // Update count for paginator based on accumulated results
                // Store separately from playbackList so it persists during playback
                const pageSize = this.paginator?.pageSize ?? 10;
                // Calculate total length: accumulated results + one extra page if more available
                // This ensures paginator shows next page button when there are more results
                if (this.hasMoreResults) {
                    // If we have more, set count to current results + one full page
                    this.paginatorCount = this.accumulatedResults.length + pageSize;
                    this.playbackList.count = this.paginatorCount;
                } else {
                    // No more results, use actual count
                    this.paginatorCount = this.accumulatedResults.length;
                    this.playbackList.count = this.paginatorCount;
                }
                
                // Log for debugging
                console.log(`Results received: ${this.playbackList.results.length} results, accumulated: ${this.accumulatedResults.length}, offset: ${batchOffset}`, this.playbackList.results);
            }

            this.resultsPending = false;
            this.form.enable();
            
            // Reset execution guard now that results have arrived
            this.isExecutingFormChange = false;

            // Always refresh display when results arrive
            // This ensures the display updates immediately, even if paginator isn't ready
            if (this.accumulatedResults.length > 0) {
                const batchOffset = this.playbackList?.options?.offset ?? 0;
                
                // For new searches (offset 0 and accumulated results were just reset), ensure paginator is on first page
                // Only reset if we have very few results (indicating this is a fresh search, not a reload of offset 0)
                if (batchOffset === 0 && this.playbackList?.results && this.accumulatedResults.length <= this.playbackList.results.length && this.paginator && this.paginator.pageIndex !== 0) {
                    this.paginator.firstPage();
                }
                
                // Always update display directly when results arrive
                // Use paginator values if available, otherwise use defaults
                const pageSize = this.paginator?.pageSize ?? 10;
                const pageIndex = this.paginator?.pageIndex ?? 0;
                const from = pageIndex * pageSize;
                const to = from + pageSize - 1;
                
                const calls: Array<RdioScannerCall | null> = this.accumulatedResults.slice(from, Math.min(to + 1, this.accumulatedResults.length));
                
                // Ensure we always have the expected number of rows for the table
                while (calls.length < this.results.value.length) {
                    calls.push(null);
                }
                
                console.log(`Display update: accumulated=${this.accumulatedResults.length}, from=${from}, to=${to}, calls.length=${calls.length}, non-null=${calls.filter(c => c !== null).length}, pageIndex=${pageIndex}, pageSize=${pageSize}`);
                
                this.results.next(calls);
                
                // Force change detection to ensure UI updates
                this.ngChangeDetectorRef.detectChanges();
                
                console.log(`Display updated: showing ${calls.filter(c => c !== null).length} calls from index ${from} to ${Math.min(to, this.accumulatedResults.length - 1)}, total accumulated: ${this.accumulatedResults.length}`);
                
                // Also call refreshResults to handle pagination logic (batch loading, etc.)
                // But only if paginator is ready - if not, the direct update above will handle display
                if (this.paginator) {
                    // Use setTimeout to avoid interfering with the direct display update
                    setTimeout(() => this.refreshResults(), 0);
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

    private getSelectedTalkgroup(): RdioScannerTalkgroup | undefined {
        const system = this.getSelectedSystem();
        if (!system) return undefined;
        const talkgroupIndex = this.form.value.talkgroup;
        if (talkgroupIndex == null || talkgroupIndex < 0) return undefined;
        return system.talkgroups.find((talkgroup) => talkgroup.label === this.optionsTalkgroup[talkgroupIndex]);
    }
}
