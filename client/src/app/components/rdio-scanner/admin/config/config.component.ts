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

import { ChangeDetectionStrategy, ChangeDetectorRef, Component, Input, OnDestroy, OnInit, ViewChild, ViewEncapsulation } from '@angular/core';
import { FormArray, FormControl, FormGroup } from '@angular/forms';
import { MatSnackBar } from '@angular/material/snack-bar';
import { AdminEvent, RdioScannerAdminService, Config, Group, Tag } from '../admin.service';
import { RdioScannerAdminUsersComponent } from './users/users.component';
import { RdioScannerAdminUserGroupsComponent } from './user-groups/user-groups.component';
import { RdioScannerAdminOptionsComponent } from './options/options.component';
import { Subscription } from 'rxjs';

@Component({
    changeDetection: ChangeDetectionStrategy.OnPush,
    encapsulation: ViewEncapsulation.None,
    selector: 'rdio-scanner-admin-config',
    styleUrls: ['./config.component.scss'],
    templateUrl: './config.component.html',
})
export class RdioScannerAdminConfigComponent implements OnDestroy, OnInit {
    /** When true the sidebar save/reset buttons are hidden (header bar owns them). */
    @Input() hideActions = false;

    docker = false;

    /** Currently active section in the sidebar nav */
    activeSection = 'user-groups';

    form: FormGroup | undefined;

    /** True while the initial config is being fetched / form being built */
    loading = true;

    /** Store original config for lazy loading */
    originalConfig: Config = {};

    /** Track which systems have had their talkgroups loaded */
    private systemsWithLoadedTalkgroups = new Set<number>();

    // ─── Systems sidebar nav state ────────────────────────────────────────────
    /** Whether the systems sub-nav is expanded in the sidebar */
    systemsNavExpanded = false;

    /** The FormGroup of the currently selected system (for detail view) */
    activeSystemForm: FormGroup | null = null;

    /** Sorted list of system FormGroups for the sidebar */
    get systemsList(): FormGroup[] {
        return (this.systems.controls as FormGroup[])
            .slice()
            .sort((a, b) => (a.value.order || 0) - (b.value.order || 0));
    }

    /** Raw group values for passing to the system component */
    get groupsValue(): Group[] {
        return this.groups?.value || [];
    }

    /** Raw tag values for passing to the system component */
    get tagsValue(): Tag[] {
        return this.tags?.value || [];
    }

    /** Raw apikey values for passing to the system component */
    get apikeysValue(): any[] {
        return this.apikeys?.value || [];
    }

    private isImportedForReview = false;

    /** Preserved from the imported config file — not part of the Angular form. */
    private importedUserAlertPreferences: any[] | null = null;
    private importedDeviceTokens: any[] | null = null;

    /**
     * Timestamp of the last reset() call. Used to suppress the WebSocket
     * config push that arrives right after the HTTP GET already built the form
     * — both carry identical data, rebuilding twice is wasted work.
     */
    private _lastResetTime = 0;

    // Track subscriptions to prevent memory leaks and duplicate subscriptions
    private groupsSubscription?: Subscription;
    private tagsSubscription?: Subscription;
    private statusSubscription?: Subscription;

    get apikeys(): FormArray {
        return (this.form?.get('apikeys') as FormArray) || new FormArray([]);
    }

    get dirwatch(): FormArray {
        return (this.form?.get('dirwatch') as FormArray) || new FormArray([]);
    }

    get downstreams(): FormArray {
        return (this.form?.get('downstreams') as FormArray) || new FormArray([]);
    }

    get groups(): FormArray {
        return (this.form?.get('groups') as FormArray) || new FormArray([]);
    }

    get options(): FormGroup {
        return (this.form?.get('options') as FormGroup) || new FormGroup({});
    }

    get systems(): FormArray {
        return (this.form?.get('systems') as FormArray) || new FormArray([]);
    }

    get tags(): FormArray {
        return (this.form?.get('tags') as FormArray) || new FormArray([]);
    }

    get users(): FormArray {
        return (this.form?.get('users') as FormArray) || new FormArray([]);
    }

    get userGroups(): FormArray {
        return (this.form?.get('userGroups') as FormArray) || new FormArray([]);
    }

    get keywordLists(): FormArray {
        return (this.form?.get('keywordLists') as FormArray) || new FormArray([]);
    }

    private set config(val: Config | undefined) {
        if (val) {
            this.originalConfig = val;
        }
    }

    private get config(): Config | undefined {
        return this.originalConfig;
    }

    private eventSubscription;

    @ViewChild(RdioScannerAdminUsersComponent) private usersComponent: RdioScannerAdminUsersComponent | undefined;
    @ViewChild(RdioScannerAdminUserGroupsComponent) private userGroupsComponent: RdioScannerAdminUserGroupsComponent | undefined;
    @ViewChild(RdioScannerAdminOptionsComponent) private optionsComponent: RdioScannerAdminOptionsComponent | undefined;

    /** Navigate to a specific options panel (called by the global search bar). */
    navigateToOption(panelName: string): void {
        this.setSection('options');
        // Defer so the options component is rendered before we try to open the panel
        setTimeout(() => this.optionsComponent?.openPanel(panelName), 80);
    }

    /** True while a single-system API save is in flight (drives the Save button state). */
    savingSystem = false;

    /** Brief flash on the Save System button after a successful save. */
    systemSaveSuccess = false;
    private systemSaveSuccessTimeout?: ReturnType<typeof setTimeout>;

    constructor(
        private adminService: RdioScannerAdminService,
        private ngChangeDetectorRef: ChangeDetectorRef,
        private matSnackBar: MatSnackBar,
    ) {
        this.eventSubscription = this.adminService.event.subscribe(async (event: AdminEvent) => {
            if ('authenticated' in event && event.authenticated === true) {
                this.config = await this.adminService.getConfig();

                this.reset();
            }

            if ('config' in event) {
                this.config = event.config;

                if (!this.form) {
                    // HTTP response hasn't arrived yet — WS beat it, build now.
                    this.reset();
                } else if (this.form.pristine) {
                    // Only rebuild from a WS push if enough time has passed since
                    // the last reset. If the WS push arrives within 5 s of the
                    // HTTP-triggered build it carries the same data — skip it.
                    const msSinceLastReset = Date.now() - this._lastResetTime;
                    if (msSinceLastReset > 5000) {
                        this.reset();
                    }
                }
            }

            if ('docker' in event) {
                this.docker = event.docker ?? false;
            }
        });
    }

    ngOnDestroy(): void {
        this.eventSubscription.unsubscribe();
        this.groupsSubscription?.unsubscribe();
        this.tagsSubscription?.unsubscribe();
        this.statusSubscription?.unsubscribe();
        if (this.systemSaveSuccessTimeout) {
            clearTimeout(this.systemSaveSuccessTimeout);
        }
    }

    async ngOnInit(): Promise<void> {
        if (!this.adminService.authenticated) {
            this.loading = false;
            return;
        }

        this.loading = true;
        this.ngChangeDetectorRef.markForCheck(); // show spinner immediately

        this.config = await this.adminService.getConfig();

        // Yield one animation frame so the browser can paint the loading
        // spinner before we synchronously build the entire form tree.
        await new Promise<void>(resolve => setTimeout(resolve, 0));

        // If the WebSocket already built the form while we were awaiting
        // the HTTP response, don't rebuild it.
        if (!this.form) {
            this.reset();
        }

        this.loading = false;
        this.ngChangeDetectorRef.markForCheck();
    }

    // ─── Section navigation ───────────────────────────────────────────────────

    setSection(section: string): void {
        // When navigating away from Systems, close the sub-nav and clear selection
        if (section !== 'systems' && section !== 'system-detail') {
            this.systemsNavExpanded = false;
            this.activeSystemForm = null;
        }
        this.activeSection = section;
        this.ngChangeDetectorRef.markForCheck();
    }

    /** Toggle the systems sub-nav and navigate to the systems overview */
    toggleSystemsSection(): void {
        this.systemsNavExpanded = !this.systemsNavExpanded;
        this.activeSystemForm = null;
        this.activeSection = 'systems';
        this.ngChangeDetectorRef.markForCheck();
    }

    /** Navigate to a specific system's detail view */
    selectSystem(systemForm: FormGroup): void {
        this.systemsNavExpanded = true;
        this.activeSystemForm = systemForm;
        this.activeSection = 'system-detail';
        this.ngChangeDetectorRef.markForCheck();
    }

    /** Add a new system and immediately navigate to its detail view */
    addNewSystem(): void {
        const system = this.adminService.newSystemForm();
        system.markAsDirty({ onlySelf: false });
        this.systems.insert(0, system);
        this.form?.markAsDirty();
        this.systemsNavExpanded = true;
        this.activeSystemForm = system;
        this.activeSection = 'system-detail';
        this.ngChangeDetectorRef.markForCheck();
    }

    /**
     * Save ONLY the currently open system via the dedicated API endpoint.
     * No page reload — the server merges this one system (preserving other systems'
     * lazily-loaded talkgroups) and broadcasts the new config to live clients.
     */
    async saveCurrentSystem(): Promise<void> {
        if (!this.activeSystemForm) return;

        if (this.activeSystemForm.invalid) {
            this.activeSystemForm.markAllAsTouched();
            this.matSnackBar.open('Fix the highlighted fields before saving.', 'Close', { duration: 4000 });
            return;
        }

        const raw = this.activeSystemForm.getRawValue();
        const system = this.convertSystemForSave(raw);

        this.savingSystem = true;
        const systems = await this.adminService.saveSystem(system);
        this.savingSystem = false;

        if (systems) {
            this.activeSystemForm.markAsPristine();
            this.applySavedSystems(systems, raw);
            // Suppress the EmitConfig echo (arrives within ~5 s) so the open system
            // view isn't torn down and rebuilt right after our own save.
            this._lastResetTime = Date.now();
            this.showSystemSaveSuccess();
        } else {
            this.matSnackBar.open('Failed to save system. Please try again.', 'Close', { duration: 4000 });
        }
        this.ngChangeDetectorRef.markForCheck();
    }

    private showSystemSaveSuccess(): void {
        const label = this.activeSystemForm?.get('label')?.value?.trim() || 'System';
        this.matSnackBar.open(`${label} saved`, 'Close', { duration: 1500 });
        this.systemSaveSuccess = true;
        if (this.systemSaveSuccessTimeout) {
            clearTimeout(this.systemSaveSuccessTimeout);
        }
        this.systemSaveSuccessTimeout = setTimeout(() => {
            this.systemSaveSuccess = false;
            this.ngChangeDetectorRef.markForCheck();
        }, 2500);
    }

    /**
     * After a single-system save, reconcile local state with the server response:
     * propagate a server-assigned id into the form (new systems) and refresh the
     * originalConfig entry so lazy-load + getSystemData stay correct without a reload.
     */
    private applySavedSystems(systems: any[], savedRaw: any): void {
        let saved = savedRaw.id ? systems.find(s => s.id === savedRaw.id) : undefined;
        if (!saved && savedRaw.systemRef != null) {
            saved = systems.find(s => s.systemRef === savedRaw.systemRef);
        }
        if (!saved) return;

        if (!savedRaw.id && saved.id) {
            this.activeSystemForm?.get('id')?.setValue(saved.id, { emitEvent: false });
        }

        if (!this.originalConfig.systems) this.originalConfig.systems = [];
        const idx = this.originalConfig.systems.findIndex(s => s.id === saved.id);
        if (idx !== -1) {
            this.originalConfig.systems[idx] = saved;
        } else {
            this.originalConfig.systems.push(saved);
        }

        if (saved.id) this.systemsWithLoadedTalkgroups.add(saved.id);
    }

    /**
     * Apply the flat-form → nested tone-set conversion (and units/talkgroup
     * restoration) for a single system. Shared by the whole-config save and the
     * per-system API save.
     */
    private convertSystemForSave(system: any): any {
        // Units are managed as raw objects (not FormGroups) — always restore
        // from originalConfig which is mutated in-place by the system component
        if (system.id) {
            const originalSystem = this.originalConfig.systems?.find(s => s.id === system.id);
            if (originalSystem) {
                system.units = originalSystem.units || [];
            }
        }

        // Restore talkgroups from original config if they weren't loaded
        if (system.id && !this.systemsWithLoadedTalkgroups.has(system.id)) {
            const originalSystem = this.originalConfig.systems?.find(s => s.id === system.id);
            if (originalSystem?.talkgroups) {
                system.talkgroups = originalSystem.talkgroups;
                return system;
            }
        }

        if (system.talkgroups) {
            system.talkgroups = system.talkgroups.map((talkgroup: any) => {
                if (talkgroup.toneSets && Array.isArray(talkgroup.toneSets)) {
                    talkgroup.toneSets = talkgroup.toneSets.map((toneSet: any) => {
                        const converted: any = {
                            id: toneSet.id,
                            label: toneSet.label,
                            tolerance: toneSet.tolerance || 10,
                        };

                        if (toneSet.aToneFrequency || toneSet.aToneMinDuration) {
                            converted.aTone = {
                                frequency: toneSet.aToneFrequency,
                                minDuration: toneSet.aToneMinDuration || 0,
                            };
                            if (toneSet.aToneMaxDuration) {
                                converted.aTone.maxDuration = toneSet.aToneMaxDuration;
                            }
                        }

                        if (toneSet.bToneFrequency || toneSet.bToneMinDuration) {
                            converted.bTone = {
                                frequency: toneSet.bToneFrequency,
                                minDuration: toneSet.bToneMinDuration || 0,
                            };
                            if (toneSet.bToneMaxDuration) {
                                converted.bTone.maxDuration = toneSet.bToneMaxDuration;
                            }
                        }

                        if (toneSet.longToneFrequency || toneSet.longToneMinDuration) {
                            converted.longTone = {
                                frequency: toneSet.longToneFrequency,
                                minDuration: toneSet.longToneMinDuration || 0,
                            };
                            if (toneSet.longToneMaxDuration) {
                                converted.longTone.maxDuration = toneSet.longToneMaxDuration;
                            }
                        }

                        // Preserve TonesToActive downstream fields
                        converted.downstreamEnabled = toneSet.downstreamEnabled || false;
                        if (toneSet.downstreamURL) {
                            converted.downstreamURL = toneSet.downstreamURL;
                        }
                        if (toneSet.downstreamAPIKey) {
                            converted.downstreamAPIKey = toneSet.downstreamAPIKey;
                        }

                        return converted;
                    });
                }
                return talkgroup;
            });
        }
        return system;
    }

    /** Remove the currently selected system and return to the overview */
    async removeCurrentSystem(): Promise<void> {
        if (!this.activeSystemForm) return;

        const id = this.activeSystemForm.get('id')?.value;
        const idx = this.systems.controls.indexOf(this.activeSystemForm);

        // Persisted systems are deleted via the API; brand-new (unsaved) systems
        // only need to be dropped from the form.
        if (id) {
            const systems = await this.adminService.deleteSystem(id);
            if (!systems) {
                return; // error already surfaced by the service
            }
            if (this.originalConfig.systems) {
                this.originalConfig.systems = this.originalConfig.systems.filter(s => s.id !== id);
            }
            this.systemsWithLoadedTalkgroups.delete(id);
            // Suppress the EmitConfig echo rebuild.
            this._lastResetTime = Date.now();
            this.matSnackBar.open('System deleted', 'Close', { duration: 1500 });
        }

        if (idx !== -1) {
            this.systems.removeAt(idx);
        }
        this.activeSystemForm = null;
        this.activeSection = 'systems';
        this.ngChangeDetectorRef.markForCheck();
    }

    /** Get the original system data for lazy loading */
    getSystemData(systemForm: FormGroup): any {
        if (!this.originalConfig.systems) return null;
        const systemId = systemForm.value.id;
        if (systemId) {
            return this.originalConfig.systems.find(s => s.id === systemId);
        }
        return null;
    }

    /** Mark that a system has loaded its talkgroups */
    markSystemTalkgroupsLoaded(systemForm: FormGroup): void {
        const systemId = systemForm.value.id;
        if (systemId) {
            this.systemsWithLoadedTalkgroups.add(systemId);
        }
    }

    /** Check if a system has loaded its talkgroups */
    hasSystemLoadedTalkgroups(systemForm: FormGroup): boolean {
        const systemId = systemForm.value.id;
        return systemId ? this.systemsWithLoadedTalkgroups.has(systemId) : false;
    }

    /** Merge units/talkgroups from an emitted config into originalConfig for save. */
    private syncLazyLoadedSystemData(config?: Config): void {
        if (!config?.systems?.length || !this.originalConfig.systems?.length) {
            return;
        }

        config.systems.forEach((importedSystem) => {
            if (!importedSystem.id) {
                return;
            }

            const originalSystem = this.originalConfig.systems!.find((s) => s.id === importedSystem.id);
            if (!originalSystem) {
                return;
            }

            if (importedSystem.units) {
                originalSystem.units = importedSystem.units;
            }

            if (importedSystem.talkgroups) {
                originalSystem.talkgroups = importedSystem.talkgroups;
            }
        });

        if (config.groups) {
            this.originalConfig.groups = config.groups;
        }

        if (config.tags) {
            this.originalConfig.tags = config.tags;
        }
    }

    // ─── Form lifecycle ───────────────────────────────────────────────────────

    reset(config = this.config, options?: { dirty?: boolean, isImport?: boolean }): void {
        // Stamp time so the WebSocket event handler can detect a recent rebuild
        // and skip the redundant second reset.
        this._lastResetTime = Date.now();

        // Tools > Import Units/Talkgroups emit a separate config copy; units and
        // talkgroups are lazy-loaded outside the form and restored from
        // originalConfig on save — merge imported data before rebuilding the form.
        this.syncLazyLoadedSystemData(config);

        // Unsubscribe from previous subscriptions to prevent duplicates
        this.groupsSubscription?.unsubscribe();
        this.tagsSubscription?.unsubscribe();
        this.statusSubscription?.unsubscribe();

        // Clear systems nav state since form is being rebuilt
        this.activeSystemForm = null;
        this.activeSection = this.activeSection === 'system-detail' ? 'systems' : this.activeSection;

        // Clear tracking of loaded talkgroups
        this.systemsWithLoadedTalkgroups.clear();

        this.form = this.adminService.newConfigForm(config);

        // Users, user groups and keyword lists are managed by their own
        // API-backed components — these FormArrays are only a shadow copy of the
        // imported config and are never edited through the config form. Imported
        // data can contain invalid values (e.g. a user with a missing/blank
        // email) that would silently mark the whole form invalid and disable the
        // Save button with no visible error anywhere in the sidebar. Disabling
        // them excludes them from form validity while getRawValue() still returns
        // their values for the full-import save path.
        this.form.get('users')?.disable({ emitEvent: false });
        this.form.get('userGroups')?.disable({ emitEvent: false });
        this.form.get('keywordLists')?.disable({ emitEvent: false });

        // Track if this reset is from an "Import for Review"
        this.isImportedForReview = options?.isImport === true;

        // Preserve imported data that lives outside the Angular form group
        if (options?.isImport) {
            this.importedUserAlertPreferences = Array.isArray((config as any)?.userAlertPreferences)
                ? (config as any).userAlertPreferences
                : null;
            this.importedDeviceTokens = Array.isArray((config as any)?.deviceTokens)
                ? (config as any).deviceTokens
                : null;
        } else {
            this.importedUserAlertPreferences = null;
            this.importedDeviceTokens = null;
        }

        this.statusSubscription = this.form.statusChanges.subscribe(() => {
            this.ngChangeDetectorRef.markForCheck();
        });

        this.groupsSubscription = this.groups.valueChanges.subscribe(() => {
            this.systems.controls.forEach((system) => {
                const talkgroups = system.get('talkgroups') as FormArray;

                talkgroups.controls.forEach((talkgroup) => {
                    const groupIds = talkgroup.get('groupIds') as FormArray;

                    groupIds.updateValueAndValidity({ onlySelf: true });

                    if (groupIds.errors) {
                        groupIds.markAsDirty({ onlySelf: true });
                    }
                });
            });
            this.ngChangeDetectorRef.markForCheck();
        });

        this.tagsSubscription = this.tags.valueChanges.subscribe(() => {
            this.systems.controls.forEach((system) => {
                const talkgroups = system.get('talkgroups') as FormArray;

                talkgroups.controls.forEach((talkgroup) => {
                    const tagId = talkgroup.get('tagId') as FormControl;

                    tagId.updateValueAndValidity({ onlySelf: true });

                    if (tagId.errors) {
                        tagId.markAsDirty({ onlySelf: true });
                    }
                });
            });
            this.ngChangeDetectorRef.markForCheck();
        });

        if (options?.dirty === true) {
            this.form.markAsDirty();
        }

        this.ngChangeDetectorRef.markForCheck();

        // Reload users and user groups components if they exist
        setTimeout(() => {
            if (this.usersComponent) {
                this.usersComponent.loadUsers();
            }
            if (this.userGroupsComponent) {
                this.userGroupsComponent.loadGroups();
            }
        }, 0);
        
        // Force revalidation of all talkgroup tagIds after form is fully initialized
        setTimeout(() => {
            this.systems.controls.forEach((system) => {
                const talkgroups = system.get('talkgroups') as FormArray;
                talkgroups.controls.forEach((talkgroup) => {
                    const tagId = talkgroup.get('tagId') as FormControl;
                    if (tagId && tagId.value) {
                        tagId.updateValueAndValidity({ emitEvent: false });
                    }
                });
            });
        }, 100);
    }

}
