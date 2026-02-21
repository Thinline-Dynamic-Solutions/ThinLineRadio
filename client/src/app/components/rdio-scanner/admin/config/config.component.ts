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

import { ChangeDetectionStrategy, ChangeDetectorRef, Component, OnDestroy, OnInit, ViewChild, ViewEncapsulation } from '@angular/core';
import { FormArray, FormControl, FormGroup } from '@angular/forms';
import { AdminEvent, RdioScannerAdminService, Config, Group, Tag } from '../admin.service';
import { RdioScannerAdminUsersComponent } from './users/users.component';
import { RdioScannerAdminUserGroupsComponent } from './user-groups/user-groups.component';
import { Subscription } from 'rxjs';

@Component({
    changeDetection: ChangeDetectionStrategy.OnPush,
    encapsulation: ViewEncapsulation.None,
    selector: 'rdio-scanner-admin-config',
    styleUrls: ['./config.component.scss'],
    templateUrl: './config.component.html',
})
export class RdioScannerAdminConfigComponent implements OnDestroy, OnInit {
    docker = false;

    /** Currently active section in the sidebar nav */
    activeSection = 'user-registration';

    form: FormGroup | undefined;

    /** True while the initial config is being fetched / form being built */
    loading = true;

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

    private config: Config | undefined;

    private eventSubscription;

    @ViewChild(RdioScannerAdminUsersComponent) private usersComponent: RdioScannerAdminUsersComponent | undefined;
    @ViewChild(RdioScannerAdminUserGroupsComponent) private userGroupsComponent: RdioScannerAdminUserGroupsComponent | undefined;

    constructor(
        private adminService: RdioScannerAdminService,
        private ngChangeDetectorRef: ChangeDetectorRef,
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
        system.markAllAsTouched();
        this.systems.insert(0, system);
        this.form?.markAsDirty();
        this.systemsNavExpanded = true;
        this.activeSystemForm = system;
        this.activeSection = 'system-detail';
        this.ngChangeDetectorRef.markForCheck();
    }

    /** Remove the currently selected system and return to the overview */
    removeCurrentSystem(): void {
        if (!this.activeSystemForm) return;
        const idx = this.systems.controls.indexOf(this.activeSystemForm);
        if (idx !== -1) {
            this.systems.removeAt(idx);
            this.form?.markAsDirty();
        }
        this.activeSystemForm = null;
        this.activeSection = 'systems';
        this.ngChangeDetectorRef.markForCheck();
    }

    // ─── Form lifecycle ───────────────────────────────────────────────────────

    reset(config = this.config, options?: { dirty?: boolean, isImport?: boolean }): void {
        // Stamp time so the WebSocket event handler can detect a recent rebuild
        // and skip the redundant second reset.
        this._lastResetTime = Date.now();

        // Unsubscribe from previous subscriptions to prevent duplicates
        this.groupsSubscription?.unsubscribe();
        this.tagsSubscription?.unsubscribe();
        this.statusSubscription?.unsubscribe();

        // Clear systems nav state since form is being rebuilt
        this.activeSystemForm = null;
        this.activeSection = this.activeSection === 'system-detail' ? 'systems' : this.activeSection;

        this.form = this.adminService.newConfigForm(config);
        
        // Track if this reset is from an "Import for Review"
        this.isImportedForReview = options?.isImport === true;

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
                        groupIds.markAsTouched({ onlySelf: true });
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
                        tagId.markAsTouched({ onlySelf: true });
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

    async save(): Promise<void> {
        this.form?.markAsPristine();

        const formValue = this.form?.getRawValue();
        
        const isFullImport = this.isImportedForReview;
        
        if (!isFullImport) {
            delete formValue.users;
            delete formValue.userGroups;
            delete formValue.keywordLists;
            delete formValue.userAlertPreferences;
            delete formValue.deviceTokens;
        }
        
        this.isImportedForReview = false;

        // Convert tone sets from flat form structure to nested structure
        if (formValue?.systems) {
            formValue.systems = formValue.systems.map((system: any) => {
                if (system.talkgroups) {
                    system.talkgroups = system.talkgroups.map((talkgroup: any) => {
                        if (talkgroup.toneSets && Array.isArray(talkgroup.toneSets)) {
                            talkgroup.toneSets = talkgroup.toneSets.map((toneSet: any) => {
                                const converted: any = {
                                    id: toneSet.id,
                                    label: toneSet.label,
                                    tolerance: toneSet.tolerance || 10,
                                };
                                
                                if (toneSet.minDuration) {
                                    converted.minDuration = toneSet.minDuration;
                                }
                                
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
                                
                                return converted;
                            });
                        }
                        return talkgroup;
                    });
                }
                return system;
            });
        }

        // Convert transcription config from form structure
        if (formValue?.options) {
            if (formValue.options.transcriptionEnabled !== undefined) {
                formValue.options.transcriptionConfig = formValue.options.transcriptionConfig || {};
                formValue.options.transcriptionConfig.enabled = formValue.options.transcriptionEnabled;
            }
            if (formValue.options.transcriptionEnabled !== undefined && formValue.options.transcriptionConfig) {
                formValue.options.transcriptionConfig.enabled = formValue.options.transcriptionEnabled;
            }
            
            if (formValue.options.transcriptionConfig?.hallucinationPatterns) {
                const patternsString = formValue.options.transcriptionConfig.hallucinationPatterns;
                if (typeof patternsString === 'string') {
                    formValue.options.transcriptionConfig.hallucinationPatterns = patternsString
                        .split('\n')
                        .map((line: string) => line.trim())
                        .filter((line: string) => line.length > 0);
                }
            }
            
            if (formValue.options.transcriptionConfig?.assemblyAIWordBoost) {
                const wordBoostString = formValue.options.transcriptionConfig.assemblyAIWordBoost;
                if (typeof wordBoostString === 'string') {
                    formValue.options.transcriptionConfig.assemblyAIWordBoost = wordBoostString
                        .split('\n')
                        .map((line: string) => line.trim())
                        .filter((line: string) => line.length > 0);
                }
            }
            
            formValue.options.relayServerURL = 'https://tlradioserver.thinlineds.com';
        }

        const updatedConfig = await this.adminService.saveConfig(formValue, isFullImport);
        
        if (updatedConfig) {
            window.location.reload();
        }
    }
}
