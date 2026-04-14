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

import { Component, EventEmitter, OnInit, Output } from '@angular/core';
import { RdioScannerAdminService } from '../../admin.service';

export interface Config {
    access?: any[];
    apikeys?: any[];
    dirwatch?: any[];
    downstreams?: any[];
    groups?: any[];
    options?: any;
    systems?: any[];
    tags?: any[];
    version?: string;
    radioReference?: any;
}

export interface RadioReferenceSystem {
    id: number;
    name: string;
    type: string;
    city: string;
    county: string;
    state: string;
    country: string;
    lastUpdated: string;
}

export interface RadioReferenceTalkgroup {
    id: number;
    alphaTag: string;
    description: string;
    group: string;
    tag: string;
    enc: number; // Match the backend field name exactly (lowercase)
}



export interface RadioReferenceSite {
    /** Radio Reference sends zero-padded or RFSS-prefixed strings (e.g. "055", "1-055"). */
    id: number | string;
    name: string;
    latitude: number;
    longitude: number;
    countyId: number;
    countyName: string;
    rfss: number;
    frequencies?: number[]; // Site frequencies
}

@Component({
    selector: 'rdio-scanner-admin-radio-reference-import',
    styleUrls: ['./radio-reference-import.component.scss'],
    templateUrl: './radio-reference-import.component.html',
})
export class RdioScannerAdminRadioReferenceImportComponent implements OnInit {
    @Output() config = new EventEmitter<Config>();
    /** Emitted after a direct-to-DB import (data already persisted, no save needed). */
    @Output() configSaved = new EventEmitter<Config>();

    baseConfig: Config = {};

    // Radio Reference connection status
    isConnected: boolean = false;
    connectionStatus: string = '';
    websocketStatus: string = '';
    hasRadioReferenceCredentials: boolean = false;

    // Import
    selectedSystem: RadioReferenceSystem | null = null;
    importType: 'talkgroups' | 'sites' = 'talkgroups';
    importData: any[] = [];
    isImporting: boolean = false;

    // Import destination
    localSystems: any[] = [];
    targetSystemId: number | null = null;

    // UI state
    isLoading: boolean = false;
    errorMessage: string = '';
    successMessage: string = '';

    // Talkgroup management
    allTalkgroups: RadioReferenceTalkgroup[] = [];
    filteredTalkgroups: RadioReferenceTalkgroup[] = [];

    // Talkgroup categories
    talkgroupCategories: { id: number, name: string, description: string }[] = [];
    selectedCategories: { id: number, name: string, description: string }[] = [];
    selectAllCategories: boolean = false;
    isLoadingCategories: boolean = false;
    hasUserSelectedCategory: boolean = false; // Track if user has made an explicit selection

    // Table configuration
    importColumns = ['id', 'alphaTag', 'description', 'group', 'tag', 'encrypted', 'action'];
    talkgroupColumns = ['select', 'id', 'alphaTag', 'description', 'group', 'tag', 'encrypted'];
    pageSize = 50;
    currentPage = 0;

    // Review list pagination (importData)
    importListPage = 0;
    importListPageSize = 50;

    // Search and filtering
    searchTerm: string = '';
    groupFilter: string = '';
    tagFilter: string = '';
    encryptedFilter: boolean | null = null;
    encryptedFilterSelection: 'all' | 'encrypted' | 'clear' = 'all';

    // Dropdown data
    countries: { id: number, name: string }[] = [];
    states: { id: number, name: string }[] = [];
    counties: { id: number, name: string }[] = [];
    systems: { id: number, name: string }[] = [];
    selectedCountry: { id: number, name: string } | null = null;
    selectedCountryId: number | null = null;
    selectedStateId: number | null = null;
    selectedCountyId: number | null = null;

    // Progress tracking
    progressCurrent: number = 0;
    progressTotal: number = 0;
    progressMessage: string = '';
    progressStatus: 'idle' | 'starting' | 'processing' | 'complete' = 'idle';
    retryCount: number = 0; // Track retry attempts
    
    // Talkgroup selection
    private selectedTalkgroupIds: Set<number> = new Set<number>();
    
    // Site selection (ids are strings from Radio Reference, e.g. "055", "1-055")
    private selectedSiteIds: Set<string> = new Set<string>();
    allSites: RadioReferenceSite[] = [];
    filteredSites: RadioReferenceSite[] = [];

    constructor(private adminService: RdioScannerAdminService) { }

    // State persistence key
    private readonly STORAGE_KEY = 'rdio-scanner-rr-import-state';

    // Save current state to localStorage
    private saveState(): void {
        const state = {
            importType: this.importType,
            targetSystemId: this.targetSystemId,
            selectedCountry: this.selectedCountry,
            selectedCountryId: this.selectedCountryId,
            selectedStateId: this.selectedStateId,
            selectedCountyId: this.selectedCountyId,
            selectedSystem: this.selectedSystem,
            countries: this.countries,
            states: this.states,
            counties: this.counties,
            systems: this.systems,
            talkgroupCategories: this.talkgroupCategories,
            selectedCategories: this.selectedCategories,
            allTalkgroups: this.allTalkgroups,
            allSites: this.allSites,
            importData: this.importData,
        };
        localStorage.setItem(this.STORAGE_KEY, JSON.stringify(state));
    }

    // Restore state from localStorage
    private restoreState(): boolean {
        const saved = localStorage.getItem(this.STORAGE_KEY);
        if (!saved) return false;

        try {
            const state = JSON.parse(saved);
            
            // Restore import settings
            this.importType = state.importType || 'talkgroups';
            this.targetSystemId = state.targetSystemId;
            
            // Restore location selections
            this.selectedCountryId = state.selectedCountryId;
            this.selectedStateId = state.selectedStateId;
            this.selectedCountyId = state.selectedCountyId;
            
            // Restore selected system
            this.selectedSystem = state.selectedSystem;
            
            // Restore dropdown data
            this.countries = state.countries || [];
            this.states = state.states || [];
            this.counties = state.counties || [];
            this.systems = state.systems || [];

            // Restore selectedCountry object — required for the State dropdown's [disabled] binding.
            // Fall back to reconstructing it from the countries list + selectedCountryId in case
            // the saved state predates the selectedCountry field being persisted.
            this.selectedCountry = state.selectedCountry ||
                (this.selectedCountryId !== null
                    ? (this.countries.find(c => c.id === this.selectedCountryId) ?? null)
                    : null);
            
            // Restore categories
            this.talkgroupCategories = state.talkgroupCategories || [];
            this.selectedCategories = state.selectedCategories || [];
            
            // Restore loaded data
            this.allTalkgroups = state.allTalkgroups || [];
            this.allSites = state.allSites || [];
            this.importData = state.importData || [];
            
            // Re-apply filters if data was loaded
            if (this.allTalkgroups.length > 0) {
                this.applyFilters();
            }
            if (this.allSites.length > 0) {
                this.applySiteFilters();
            }
            
            return true;
        } catch (e) {
            console.error('Failed to restore Radio Reference state:', e);
            return false;
        }
    }

    // Clear saved state
    clearSavedState(): void {
        localStorage.removeItem(this.STORAGE_KEY);
    }

    onCredentialsChange(): void {
        // Clear connection status when credentials change
        this.isConnected = false;
        this.connectionStatus = '';
        this.countries = [];
        this.states = [];
        this.counties = [];
        this.systems = [];
        this.selectedCountry = null;
        this.selectedCountryId = null;
        this.selectedStateId = null;
        this.selectedCountyId = null;
        this.allTalkgroups = [];
        this.filteredTalkgroups = [];
        this.errorMessage = '';
        this.clearProgress();
    }
    
    clearProgress(): void {
        this.progressCurrent = 0;
        this.progressTotal = 0;
        this.progressMessage = '';
        this.progressStatus = 'idle';
        this.retryCount = 0;
    }

    ngOnInit(): void {
        // Only load data if user is authenticated
        if (this.adminService.authenticated) {
            // Try to restore previous state first
            const stateRestored = this.restoreState();
            
            this.loadConfig(); // Load configuration including RadioReference credentials
            this.checkWebSocketStatus();
            
            // Set up periodic WebSocket status check
            setInterval(() => {
                this.checkWebSocketStatus();
            }, 10000); // Check every 10 seconds

            if (stateRestored) {
                console.log('Radio Reference state restored from previous session');
            }
        }
    }

    onTargetSystemIdChange(value: any): void {
        this.targetSystemId = this.normalizeSystemId(value);
        this.saveState(); // Save state
    }

    async loadConfig(): Promise<void> {
        this.baseConfig = await this.adminService.getConfig();
        
        if (this.baseConfig.options?.radioReferenceEnabled && this.baseConfig.options?.radioReferenceUsername) {
            this.hasRadioReferenceCredentials = true;
        } else {
            this.hasRadioReferenceCredentials = false;
        }

        this.localSystems = Array.isArray(this.baseConfig.systems) ? this.baseConfig.systems : [];
        if (this.localSystems.length > 0) {
            const existing = this.localSystems.find((s: any) => this.normalizeSystemId(s.id) === this.targetSystemId);
            const candidate = existing ? this.normalizeSystemId(existing.id) : this.normalizeSystemId(this.localSystems[0]?.id);
            this.targetSystemId = candidate ?? null;
        } else if (this.localSystems.length === 0) {
            this.targetSystemId = null;
        }

        // Countries will be loaded when connection is tested successfully
    }

    checkWebSocketStatus(): void {
        const ws = this.adminService['configWebSocket'];
        if (ws && ws.readyState === WebSocket.OPEN) {
            this.websocketStatus = 'Connected';
        } else if (ws && ws.readyState === WebSocket.CONNECTING) {
            this.websocketStatus = 'Connecting...';
        } else if (ws && ws.readyState === WebSocket.CLOSING) {
            this.websocketStatus = 'Disconnecting...';
        } else {
            this.websocketStatus = 'Disconnected';
        }
    }

    // RadioReference credentials are now managed in the main admin settings
    // This component automatically uses the saved credentials from options

    async testConnection(): Promise<void> {
        if (!this.hasRadioReferenceCredentials) {
            this.connectionStatus = 'Radio Reference credentials not configured. Please configure them in the main admin settings first.';
            return;
        }

        const username = this.baseConfig.options?.radioReferenceUsername;

        if (!username) {
            this.connectionStatus = 'Radio Reference username is missing. Please check the admin settings.';
            return;
        }

        try {
            const response = await this.adminService.testRadioReferenceConnection(username);
            if (response.success) {
                this.isConnected = true;
                this.connectionStatus = `Connected successfully! Account expires: ${response.userInfo.expirationDate}`;
                
                // Now load countries since we're connected
                await this.loadCountries();
            } else {
                this.isConnected = false;
                this.connectionStatus = 'Connection failed';
            }
        } catch (error: any) {
            this.isConnected = false;
            this.connectionStatus = `Connection failed: ${error.error || error.message}`;
        }
    }

    // ----- Dropdown flow -----
    async loadCountries(): Promise<void> {
        try {
            const res = await this.adminService.rrGetCountries();
            this.countries = res?.items || [];
            this.saveState(); // Save state
        } catch (error) {
            console.error('Error loading countries:', error);
        }
    }

    async onCountryChange(): Promise<void> {
        if (this.selectedCountry) {
            this.selectedCountryId = this.selectedCountry.id;
        } else {
            this.selectedCountryId = null;
        }

        this.saveState();

        this.states = [];
        this.counties = [];
        this.systems = [];
        this.selectedStateId = null;
        this.selectedCountyId = null;
        this.selectedSystem = null;
        this.allTalkgroups = [];
        this.filteredTalkgroups = [];
        this.errorMessage = '';

        if (this.selectedCountryId) {
            console.log('[RR] Fetching states for countryId:', this.selectedCountryId);
            try {
                const res = await this.adminService.rrGetStates(this.selectedCountryId);
                console.log('[RR] rrGetStates response:', res);
                if (res?.items?.length > 0) {
                    this.states = res.items;
                    console.log('[RR] Loaded', this.states.length, 'states');
                } else {
                    this.errorMessage = res?.error
                        ? `Failed to load states: ${res.error}`
                        : 'No states returned for this country. Check the browser Network tab for the /radioreference/states response.';
                    console.warn('[RR] No states in response:', res);
                }
            } catch (err) {
                this.errorMessage = `Error loading states: ${err}`;
                console.error('[RR] rrGetStates threw:', err);
            }
            this.saveState();
        }
    }

    async onStateChange(): Promise<void> {
        this.counties = [];
        this.systems = [];
        this.selectedCountyId = null;
        this.selectedSystem = null;
        this.allTalkgroups = [];
        this.filteredTalkgroups = [];
        
        this.saveState(); // Save state
        
        if (this.selectedStateId) {
            const res = await this.adminService.rrGetCounties(this.selectedStateId);
            this.counties = res?.items || [];
            this.saveState(); // Save state after loading counties
        }
    }

    async onCountyChange(): Promise<void> {
        this.systems = [];
        this.selectedSystem = null;
        this.allTalkgroups = [];
        this.filteredTalkgroups = [];
        
        if (this.selectedCountyId) {
            const res = await this.adminService.rrGetSystems(this.selectedCountyId);
            this.systems = res?.items || [];
            this.saveState(); // Save state after loading systems
        } else {
            this.saveState(); // Save state even if no county selected
        }
    }

    onImportTypeChange(): void {
        // Clear all data when switching import types
        this.allTalkgroups = [];
        this.filteredTalkgroups = [];
        this.selectedCategories = [];
        this.talkgroupCategories = [];
        this.importData = [];
        this.errorMessage = '';
        this.hasUserSelectedCategory = false; // Reset user selection flag
        
        // Load appropriate data based on import type if we have a system selected
        if (this.selectedSystem) {
            if (this.importType === 'talkgroups') {
                this.loadTalkgroupCategories();
            } else if (this.importType === 'sites') {
                this.loadSites();
            }
        }
    }

    onSystemSelectFromDropdown(systemId: number): void {
        const sys = this.systems.find(s => s.id === systemId);
        if (sys) {
            this.selectedSystem = {
                id: sys.id,
                name: sys.name,
                type: '',
                city: '',
                county: '',
                state: '',
                country: '',
                lastUpdated: ''
            };
            this.importData = [];
            this.selectedCategories = [];
            this.hasUserSelectedCategory = false; // Reset user selection flag
            
            // Always load talkgroup categories for new system selection
            if (this.importType === 'talkgroups') {
                this.loadTalkgroupCategories();
            } else if (this.importType === 'sites' && this.importData.length === 0) {
                this.loadSites();
            }
        }
        
        this.saveState(); // Save state
    }

    selectSystem(system: RadioReferenceSystem): void {
        this.selectedSystem = system;
        this.importData = [];
        this.selectedCategories = [];
        this.allTalkgroups = [];
        this.filteredTalkgroups = [];
        this.hasUserSelectedCategory = false; // Reset user selection flag
        
        // Always load talkgroup categories for new system selection
        if (this.importType === 'talkgroups') {
            this.loadTalkgroupCategories();
        } else if (this.importType === 'sites' && this.importData.length === 0) {
            this.loadSites();
        }
        
        this.saveState(); // Save state
    }

    async importDataFromRR(): Promise<void> {
        if (!this.selectedSystem) return;

        if (this.importType === 'talkgroups') {
            await this.loadTalkgroups();
            return;
        }

        this.isImporting = true;
        this.clearProgress();
        try {
            let response;
            
            if (this.importType === 'sites') {
                // Use the new sites method
                response = await this.adminService.getRadioReferenceSites(this.selectedSystem.id);
            } else {
                // Use the old method for other import types
                response = await this.adminService.importRadioReferenceData(
                    this.selectedSystem.id,
                    this.importType
                );
            }
            
            if (response.success && Array.isArray(response.data)) {
                this.importData = response.data;
                this.successMessage = '';
            }
        } catch (error: any) {
            console.error('Import failed:', error);
            this.errorMessage = `Import failed: ${error.message}`;
        } finally {
            this.isImporting = false;
        }
    }

    async importToConfig(): Promise<void> {
        if (!this.selectedSystem || this.importData.length === 0) return;

        this.isImporting = true;
        this.errorMessage = '';
        this.successMessage = '';

        try {
            let importStats = { updated: 0, created: 0 };

            switch (this.importType) {
                case 'talkgroups':
                    importStats = await this.importTalkgroups();
                    break;
                case 'sites':
                    importStats = await this.importSites();
                    break;
            }

            const parts = [];
            if (importStats.updated > 0) parts.push(`${importStats.updated} updated`);
            if (importStats.created > 0) parts.push(`${importStats.created} created`);
            this.successMessage = `Successfully imported ${this.importType}: ${parts.join(', ')}`;

            this.importData = [];
            this.importListPage = 0;

            // Reload the fresh config from the server, then tell the parent to rebuild
            // the form as already-saved (dirty: false) — the DB write already happened.
            await this.loadConfig();
            this.configSaved.emit(this.baseConfig);
        } catch (error: any) {
            console.error('Import failed:', error);
            this.errorMessage = `Import failed: ${error.message}`;
        } finally {
            this.isImporting = false;
        }
    }

    private async importTalkgroups(): Promise<{ updated: number, created: number }> {
        if (this.targetSystemId == null || !Number.isFinite(Number(this.targetSystemId))) {
            throw new Error('No target system selected');
        }

        const result = await this.adminService.rrImportToSystem(Number(this.targetSystemId), this.importData);

        if (!result.success) {
            throw new Error(result.error || 'Import failed');
        }

        return { updated: result.updated ?? 0, created: result.created ?? 0 };
    }



    private async importSites(): Promise<{ updated: number, created: number }> {
        if (this.targetSystemId == null || !Number.isFinite(Number(this.targetSystemId))) {
            throw new Error('No target system selected');
        }

        const result = await this.adminService.rrImportToSystem(Number(this.targetSystemId), [], this.importData);

        if (!result.success) {
            throw new Error(result.error || 'Import failed');
        }

        return { updated: result.updated ?? 0, created: result.created ?? 0 };
    }

    resetSearch(): void {
        this.selectedSystem = null;
        this.importData = [];
    }

    removeImportItem(index: number): void {
        this.importData.splice(index, 1);
    }

    // Talkgroup management methods
    async loadTalkgroupCategories(): Promise<void> {
        if (!this.selectedSystem) return;

        try {
            this.isLoadingCategories = true;
            this.errorMessage = '';
            
            // Get talkgroup categories for the selected system
            const response = await this.adminService.getRadioReferenceTalkgroupCategories(this.selectedSystem.id);
            
            if (response && response.success && response.categories) {
                // Sort categories alphabetically by name
                this.talkgroupCategories = response.categories.sort((a: any, b: any) => 
                    a.name.localeCompare(b.name)
                );
                this.selectedCategories = []; // Reset selection
                this.selectAllCategories = false;
                this.hasUserSelectedCategory = false; // Reset user selection flag
                this.selectedTalkgroupIds.clear();
            } else {
                this.talkgroupCategories = [];
                this.selectedCategories = [];
                this.selectAllCategories = false;
                this.selectedTalkgroupIds.clear();
                if (response && response.error) {
                    this.errorMessage = response.error;
                }
            }
            
            this.isLoadingCategories = false;
        } catch (error) {
            this.isLoadingCategories = false;
            this.errorMessage = 'Failed to load talkgroup categories: ' + error;
            this.talkgroupCategories = [];
            this.selectedCategories = [];
            this.selectAllCategories = false;
            this.selectedTalkgroupIds.clear();
        } finally {
            this.saveState(); // Save state
        }
    }

    async loadSites(): Promise<void> {
        if (!this.selectedSystem) return;

        try {
            this.isImporting = true;
            this.errorMessage = '';
            this.selectedSiteIds.clear();
            
            // Get sites for the selected system
            const response = await this.adminService.getRadioReferenceSites(this.selectedSystem.id);
            
            if (response && response.success && response.data) {
                this.allSites = response.data;
                this.applySiteFilters(); // Apply filters to populate filteredSites
            } else {
                this.allSites = [];
                this.filteredSites = [];
                if (response && response.error) {
                    this.errorMessage = response.error;
                }
            }
            
            this.isImporting = false;
        } catch (error) {
            this.isImporting = false;
            this.errorMessage = 'Failed to load sites: ' + error;
            this.allSites = [];
            this.filteredSites = [];
        } finally {
            this.saveState(); // Save state
        }
    }

    async loadTalkgroups(): Promise<void> {
        if (!this.selectedSystem || (this.selectedCategories.length === 0 && !this.selectAllCategories)) return;

        // Prevent multiple simultaneous calls
        if (this.isLoading) {
            return;
        }

        try {
            this.isLoading = true;
            this.errorMessage = '';
            this.progressStatus = 'starting';
            this.progressCurrent = 0;
            this.progressTotal = 0;
            this.retryCount = 0;
            this.selectedTalkgroupIds.clear();
            this.successMessage = '';
            
            // Set appropriate loading message based on selection
            if (this.selectAllCategories) {
                this.progressMessage = 'Processing talkgroups. Depending on how many talkgroups there are, this could take a few minutes...';
            } else {
                this.progressMessage = 'Loading talkgroups...';
            }
            
            // Start periodic WebSocket status check
            const statusCheckInterval = setInterval(() => {
                this.checkWebSocketStatus();
                if (this.websocketStatus === 'Disconnected') {
                    console.warn('WebSocket connection lost during import!');
                    this.errorMessage = 'WebSocket connection lost. Please refresh the page and try again.';
                    clearInterval(statusCheckInterval);
                }
            }, 5000); // Check every 5 seconds
            
            let response;
            
            if (this.selectAllCategories) {
                // Get all talkgroups from all categories
                response = await this.adminService.importRadioReferenceData(
                    this.selectedSystem.id,
                    'talkgroups'
                );
            } else if (this.selectedCategories.length > 0) {
                // Load talkgroups from all selected categories
                const allTalkgroups: any[] = [];
                
                for (const category of this.selectedCategories) {
                    const categoryResponse = await this.adminService.getRadioReferenceTalkgroupsByCategory(
                        this.selectedSystem.id, 
                        category.id, 
                        category.name
                    );
                    
                    if (categoryResponse && categoryResponse.success !== false && categoryResponse.data) {
                        allTalkgroups.push(...categoryResponse.data);
                    }
                }
                
                response = { success: true, data: allTalkgroups };
            } else {
                this.isLoading = false;
                return;
            }
            
            // Clear status check interval
            clearInterval(statusCheckInterval);
            
            if (response && response.success !== false) {
                if (response.data && Array.isArray(response.data)) {
                    this.allTalkgroups = response.data;
                } else {
                    this.allTalkgroups = [];
                }
                this.applyFilters();
            } else {
                console.error('Failed to load talkgroups:', response);
                this.errorMessage = response?.error || 'Failed to load talkgroups';
                this.allTalkgroups = [];
                this.filteredTalkgroups = [];
            }
        } catch (error: any) {
            console.error('Error loading talkgroups:', error);
            this.errorMessage = error.message || 'An error occurred while loading talkgroups';
            this.allTalkgroups = [];
            this.filteredTalkgroups = [];
        } finally {
            this.isLoading = false;
            this.saveState(); // Save state
        }
    }

    toggleCategorySelection(category: { id: number, name: string, description: string }): void {
        const index = this.selectedCategories.findIndex(c => c.id === category.id);
        
        if (index > -1) {
            // Remove from selection
            this.selectedCategories.splice(index, 1);
        } else {
            // Add to selection
            this.selectedCategories.push(category);
        }

        this.selectedTalkgroupIds.clear();
        this.successMessage = '';
        this.errorMessage = '';
        this.encryptedFilterSelection = 'all';
        this.encryptedFilter = null;

        if (this.selectedCategories.length === 0 && !this.selectAllCategories) {
            this.hasUserSelectedCategory = false;
            this.progressStatus = 'idle';
            this.progressMessage = '';
            this.allTalkgroups = [];
            this.filteredTalkgroups = [];
            return;
        }

        // Mark that user has made an explicit selection
        this.hasUserSelectedCategory = true;
        
        // Load talkgroups for selected categories
        this.loadTalkgroups();
        
        this.saveState(); // Save state
    }

    toggleSelectAllCategories(): void {
        // Note: selectAllCategories is already updated by [(ngModel)] before this handler fires,
        // so we read the current value rather than toggling it again.
        if (this.selectAllCategories) {
            // When selecting all, clear individual selections
            this.selectedCategories = [];
        }

        this.selectedTalkgroupIds.clear();
        this.successMessage = '';
        this.errorMessage = '';
        this.encryptedFilterSelection = 'all';
        this.encryptedFilter = null;

        if (!this.selectAllCategories && this.selectedCategories.length === 0) {
            this.hasUserSelectedCategory = false;
            this.progressStatus = 'idle';
            this.progressMessage = '';
            this.allTalkgroups = [];
            this.filteredTalkgroups = [];
            return;
        }

        // Mark that user has made an explicit selection
        this.hasUserSelectedCategory = true;
        
        // Load talkgroups
        this.loadTalkgroups();
        
        this.saveState(); // Save state
    }

    isCategorySelected(category: { id: number, name: string, description: string }): boolean {
        return this.selectedCategories.some(c => c.id === category.id);
    }

    applyFilters(): void {
        let filtered = [...this.allTalkgroups];

        // Apply search term filter
        if (this.searchTerm) {
            const term = this.searchTerm.toLowerCase();
            filtered = filtered.filter(tg => 
                tg.alphaTag.toLowerCase().includes(term) ||
                tg.description.toLowerCase().includes(term) ||
                tg.group.toLowerCase().includes(term) ||
                tg.tag.toLowerCase().includes(term) ||
                tg.id.toString().includes(term)
            );
        }

        // Apply group filter
        if (this.groupFilter) {
            filtered = filtered.filter(tg => tg.group.toLowerCase().includes(this.groupFilter.toLowerCase()));
        }

        // Apply tag filter
        if (this.tagFilter) {
            filtered = filtered.filter(tg => tg.tag.toLowerCase().includes(this.tagFilter.toLowerCase()));
        }

        // Apply encrypted filter
        if (this.encryptedFilter !== null) {
            filtered = filtered.filter(tg => this.isEncrypted(tg) === this.encryptedFilter);
        }

        this.filteredTalkgroups = filtered;
        this.currentPage = 0;
    }

    onSearchChange(): void {
        if (this.importType === 'talkgroups') {
            this.applyFilters();
            this.selectedTalkgroupIds.clear();
        } else if (this.importType === 'sites') {
            this.applySiteFilters();
            this.selectedSiteIds.clear();
        }
    }

    onGroupFilterChange(): void {
        this.applyFilters();
    }

    onTagFilterChange(): void {
        this.applyFilters();
    }

    onEncryptedFilterChange(): void {
        switch (this.encryptedFilterSelection) {
            case 'encrypted':
                this.encryptedFilter = true;
                break;
            case 'clear':
                this.encryptedFilter = false;
                break;
            default:
                this.encryptedFilter = null;
                break;
        }
        this.applyFilters();
    }

    getPaginatedTalkgroups(): RadioReferenceTalkgroup[] {
        const startIndex = this.currentPage * this.pageSize;
        return this.filteredTalkgroups.slice(startIndex, startIndex + this.pageSize);
    }

    onPageChange(event: any): void {
        this.currentPage = event.pageIndex;
        this.pageSize = event.pageSize;
    }

    getCurrentPageInfo(): string {
        const start = this.currentPage * this.pageSize + 1;
        let end, total;
        
        if (this.importType === 'sites') {
            end = Math.min((this.currentPage + 1) * this.pageSize, this.filteredSites.length);
            total = this.filteredSites.length;
        } else {
            end = Math.min((this.currentPage + 1) * this.pageSize, this.filteredTalkgroups.length);
            total = this.filteredTalkgroups.length;
        }
        
        return `${start}-${end} of ${total}`;
    }

    isTalkgroupSelected(talkgroup: RadioReferenceTalkgroup): boolean {
        return this.selectedTalkgroupIds.has(talkgroup.id);
    }

    onTalkgroupCheckboxChange(talkgroup: RadioReferenceTalkgroup, checked: boolean): void {
        if (checked) {
            this.selectedTalkgroupIds.add(talkgroup.id);
        } else {
            this.selectedTalkgroupIds.delete(talkgroup.id);
        }
    }

    areAllVisibleTalkgroupsSelected(): boolean {
        const visible = this.getPaginatedTalkgroups();
        return visible.length > 0 && visible.every(tg => this.selectedTalkgroupIds.has(tg.id));
    }

    areSomeVisibleTalkgroupsSelected(): boolean {
        const visible = this.getPaginatedTalkgroups();
        const selectedCount = visible.filter(tg => this.selectedTalkgroupIds.has(tg.id)).length;
        return selectedCount > 0 && selectedCount < visible.length;
    }

    toggleSelectAllVisible(checked: boolean): void {
        const visible = this.getPaginatedTalkgroups();
        if (checked) {
            visible.forEach(tg => this.selectedTalkgroupIds.add(tg.id));
        } else {
            visible.forEach(tg => this.selectedTalkgroupIds.delete(tg.id));
        }
    }

    selectAllTalkgroups(): void {
        this.filteredTalkgroups.forEach(tg => this.selectedTalkgroupIds.add(tg.id));
    }

    selectAllVisibleTalkgroups(): void {
        this.toggleSelectAllVisible(true);
    }

    clearSelectedTalkgroups(): void {
        this.selectedTalkgroupIds.clear();
        this.successMessage = '';
    }

    getSelectedTalkgroupCount(): number {
        return this.selectedTalkgroupIds.size;
    }

    // Site selection and management methods
    private siteKey(site: RadioReferenceSite): string {
        return String(site.id);
    }

    isSiteSelected(site: RadioReferenceSite): boolean {
        return this.selectedSiteIds.has(this.siteKey(site));
    }

    onSiteCheckboxChange(site: RadioReferenceSite, checked: boolean): void {
        const k = this.siteKey(site);
        if (checked) {
            this.selectedSiteIds.add(k);
        } else {
            this.selectedSiteIds.delete(k);
        }
    }

    areAllVisibleSitesSelected(): boolean {
        const visible = this.getPaginatedSites();
        return visible.length > 0 && visible.every(s => this.selectedSiteIds.has(this.siteKey(s)));
    }

    areSomeVisibleSitesSelected(): boolean {
        const visible = this.getPaginatedSites();
        const selectedCount = visible.filter(s => this.selectedSiteIds.has(this.siteKey(s))).length;
        return selectedCount > 0 && selectedCount < visible.length;
    }

    toggleSelectAllVisibleSites(checked: boolean): void {
        const visible = this.getPaginatedSites();
        if (checked) {
            visible.forEach(s => this.selectedSiteIds.add(this.siteKey(s)));
        } else {
            visible.forEach(s => this.selectedSiteIds.delete(this.siteKey(s)));
        }
    }

    selectAllSites(): void {
        this.filteredSites.forEach(s => this.selectedSiteIds.add(this.siteKey(s)));
    }

    selectAllVisibleSites(): void {
        this.toggleSelectAllVisibleSites(true);
    }

    clearSelectedSites(): void {
        this.selectedSiteIds.clear();
        this.successMessage = '';
    }

    getSelectedSiteCount(): number {
        return this.selectedSiteIds.size;
    }

    getPaginatedSites(): RadioReferenceSite[] {
        const startIndex = this.currentPage * this.pageSize;
        return this.filteredSites.slice(startIndex, startIndex + this.pageSize);
    }

    addSelectedSitesToImportList(): void {
        if (this.getSelectedSiteCount() === 0) {
            this.errorMessage = 'Please select at least one site to add to import list';
            return;
        }

        // Get the selected sites from filteredSites
        const sitesToAdd = this.filteredSites.filter(site => this.selectedSiteIds.has(this.siteKey(site)));

        // Add to import data, avoiding duplicates
        sitesToAdd.forEach(site => {
            if (!this.importData.some((s: any) => String(s.id) === this.siteKey(site))) {
                this.importData.push(site);
            }
        });

        this.successMessage = `Added ${sitesToAdd.length} site(s) to import list. Total: ${this.importData.length}`;
        this.errorMessage = '';
        this.clearSelectedSites();
        this.saveState();
    }

    removeSiteFromImportList(site: RadioReferenceSite): void {
        this.importData = this.importData.filter((s: any) => String(s.id) !== this.siteKey(site));
        this.saveState();
    }

    applySiteFilters(): void {
        let filtered = this.allSites;

        // Search filter (search by ID, name, county)
        if (this.searchTerm) {
            const term = this.searchTerm.toLowerCase();
            filtered = filtered.filter(site =>
                site.id.toString().includes(term) ||
                site.name.toLowerCase().includes(term) ||
                site.countyName.toLowerCase().includes(term)
            );
        }

        this.filteredSites = filtered;
        this.currentPage = 0;
    }

    addSelectedTalkgroupsToImportList(): void {
        if (this.getSelectedTalkgroupCount() === 0) {
            return;
        }

        const selectedTalkgroups = this.allTalkgroups.filter(tg => this.selectedTalkgroupIds.has(tg.id));
        let addedCount = 0;
        selectedTalkgroups.forEach(tg => {
            if (!this.importData.some(item => item.id === tg.id)) {
                this.importData.push({ ...tg });
                addedCount++;
            }
        });

        this.selectedTalkgroupIds.clear();

        if (addedCount > 0) {
            this.importListPage = 0;
            this.successMessage = `Added ${addedCount} talkgroup${addedCount > 1 ? 's' : ''} to the review list.`;
            this.errorMessage = '';
        } else {
            this.successMessage = '';
        }
    }

    getPaginatedImportData(): any[] {
        const start = this.importListPage * this.importListPageSize;
        return this.importData.slice(start, start + this.importListPageSize);
    }

    onImportListPageChange(event: any): void {
        this.importListPage = event.pageIndex;
        this.importListPageSize = event.pageSize;
    }

    clearReviewList(): void {
        if (this.importData.length === 0) {
            return;
        }
        this.importData = [];
        this.importListPage = 0;
        this.successMessage = '';
        this.errorMessage = '';
    }

    getImportButtonLabel(): string {
        if (this.isImporting) {
            return 'Importing...';
        }
        return 'Import to System';
    }

    trackBySystemId = (_: number, system: any): number => {
        return this.normalizeSystemId(system?.id) ?? 0;
    };

    private normalizeSystemId(value: any): number | null {
        const parsed = Number(value);
        return Number.isFinite(parsed) ? parsed : null;
    }

    private getNextSystemId(): number {
        if (!Array.isArray(this.baseConfig.systems) || this.baseConfig.systems.length === 0) {
            return 1;
        }
        const ids = this.baseConfig.systems.map((s: any) => Number(s.id) || 0);
        return Math.max(0, ...ids) + 1;
    }

    isEncrypted(talkgroup: RadioReferenceTalkgroup): boolean {
        return talkgroup.enc === 1 || talkgroup.enc === 2;
    }

    getEncryptedText(talkgroup: RadioReferenceTalkgroup): string {
        return this.isEncrypted(talkgroup) ? 'Yes' : 'No';
    }

    getEncryptedIcon(talkgroup: RadioReferenceTalkgroup): string {
        return this.isEncrypted(talkgroup) ? 'lock' : 'lock_open';
    }

    getEncryptedColor(talkgroup: RadioReferenceTalkgroup): string {
        return this.isEncrypted(talkgroup) ? 'warn' : 'primary';
    }

    clearFilters(): void {
        this.searchTerm = '';
        this.groupFilter = '';
        this.tagFilter = '';
        this.encryptedFilter = null;
        this.encryptedFilterSelection = 'all';
        
        if (this.importType === 'sites') {
            this.applySiteFilters();
        } else {
            this.applyFilters();
        }
    }

    exportToCsv(): void {
        if (this.filteredTalkgroups.length === 0) return;

        const headers = ['ID', 'Alpha Tag', 'Description', 'Group', 'Tag', 'Encrypted'];
        const csvContent = [
            headers.join(','),
            ...this.filteredTalkgroups.map(tg => [
                tg.id,
                `"${tg.alphaTag}"`,
                `"${tg.description}"`,
                `"${tg.group}"`,
                `"${tg.tag}"`,
                this.isEncrypted(tg) ? 'Yes' : 'No'
            ].join(','))
        ].join('\n');

        const blob = new Blob([csvContent], { type: 'text/csv' });
        const url = window.URL.createObjectURL(blob);
        const a = document.createElement('a');
        a.href = url;
        
        // Get county name from the counties array using selectedCountyId
        const countyName = this.selectedCountyId ? 
            this.counties.find(c => c.id === this.selectedCountyId)?.name || 'unknown' : 'unknown';
        
        a.download = `talkgroups_${countyName}_${this.selectedSystem?.name || 'unknown'}.csv`;
        a.click();
        window.URL.revokeObjectURL(url);
    }

    getCountyPath(): string {
        const parts = [];
        
        // Get country name from the countries array using selectedCountry
        if (this.selectedCountry) parts.push(this.selectedCountry.name);
        
        // Get state name from the states array using selectedStateId
        if (this.selectedStateId) {
            const state = this.states.find(s => s.id === this.selectedStateId);
            if (state) parts.push(state.name);
        }
        
        // Get county name from the counties array using selectedCountyId
        if (this.selectedCountyId) {
            const county = this.counties.find(c => c.id === this.selectedCountyId);
            if (county) parts.push(county.name);
        }
        
        return parts.join(' > ');
    }

    getImportTypeDisplayName(): string {
        switch (this.importType) {
            case 'talkgroups':
                return 'Talkgroups';
            case 'sites':
                return 'Sites';
            default:
                return 'Data';
        }
    }

    isCheckpointMessage(message: string | null): boolean {
        return message ? message.includes('Checkpoint') : false;
    }
}
