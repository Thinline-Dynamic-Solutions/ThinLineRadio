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

import { ChangeDetectionStrategy, Component, Inject, ViewEncapsulation } from '@angular/core';
import { FormArray, FormBuilder, FormControl, FormGroup } from '@angular/forms';
import { MAT_DIALOG_DATA, MatDialogRef } from '@angular/material/dialog';
import { debounceTime } from 'rxjs/operators';

interface System {
    all: boolean;
    id: number;
    talkgroups: Talkgroup[];
}

interface Talkgroup {
    checked: boolean;
    id: number;
}

@Component({
    changeDetection: ChangeDetectionStrategy.OnPush,
    encapsulation: ViewEncapsulation.None,
    selector: 'rdio-scanner-admin-systems-selection',
    styleUrls: ['./select.component.scss'],
    templateUrl: './select.component.html',
})
export class RdioScannerAdminSystemsSelectComponent {
    indeterminate = {
        everything: false,
        groups: [] as boolean[],
        systems: [] as boolean[],
        tags: [] as boolean[],
    };

    select: FormGroup;

    configTalkgroups: FormGroup[][];

    access!: FormGroup;

    // Track which systems are expanded (all collapsed by default for performance)
    expandedSystems: boolean[] = [];

    trackByIndex(index: number): number {
        return index;
    }

    trackBySystemId(index: number, system: FormGroup): any {
        return system.get('systemRef')?.value || index;
    }

    trackByGroupId(index: number, group: FormGroup): any {
        return group.get('id')?.value || index;
    }

    trackByTagId(index: number, tag: FormGroup): any {
        return tag.value?.id || index;
    }

    trackByTalkgroupId(index: number, talkgroup: FormGroup): any {
        return talkgroup.get('talkgroupRef')?.value || index;
    }

    toggleSystem(index: number): void {
        this.expandedSystems[index] = !this.expandedSystems[index];
    }

    expandAll(): void {
        this.expandedSystems = this.configSystems.map(() => true);
    }

    collapseAll(): void {
        this.expandedSystems = this.configSystems.map(() => false);
    }

    get configGroups(): FormGroup[] {
        const faGroups = this.access.root.get('groups') as FormArray;

        return faGroups.controls as FormGroup[];
    }

    get configSystems(): FormGroup[] {
        const faSystems = this.access.root.get('systems') as FormArray;

        return faSystems.controls as FormGroup[];
    }

    get configTags(): FormGroup[] {
        const faTags = this.access.root.get('tags') as FormArray;

        return faTags.controls as FormGroup[];
    }

    constructor(
        @Inject(MAT_DIALOG_DATA) dialogData: { access: FormGroup; rawSystems?: any[] } | FormGroup,
        private matDialogRef: MatDialogRef<RdioScannerAdminSystemsSelectComponent>,
        private ngFormBuilder: FormBuilder,
    ) {
        // Support both legacy callers (passing FormGroup directly) and new callers
        // (passing { access, rawSystems }) so all dialog callers work correctly.
        const rawSystems: any[] | undefined = (dialogData as any)?.rawSystems;
        this.access = (dialogData as any)?.access instanceof FormGroup
            ? (dialogData as any).access
            : dialogData as FormGroup;

        this.configTalkgroups = this.configSystems.map((fgSystem) => {
            const faTalkgroups = fgSystem.get('talkgroups') as FormArray;

            if ((faTalkgroups?.length ?? 0) > 0) {
                return faTalkgroups.controls as FormGroup[];
            }

            // Talkgroups were skipped at config load time for performance.
            // Use the raw systems data when available to populate the list.
            if (rawSystems) {
                const systemRef = fgSystem.get('systemRef')?.value;
                const rawSystem = rawSystems.find((s: any) => s.systemRef === systemRef);
                if (rawSystem?.talkgroups?.length) {
                    return (rawSystem.talkgroups as any[]).map((tg: any) =>
                        this.ngFormBuilder.group({
                            groupIds: this.ngFormBuilder.control(tg.groupIds || []),
                            label: this.ngFormBuilder.control(tg.label || ''),
                            talkgroupRef: this.ngFormBuilder.control(tg.talkgroupRef),
                            tagId: this.ngFormBuilder.control(tg.tagId),
                        })
                    );
                }
            }

            return [];
        });

        // Initialize all systems as collapsed for better performance
        this.expandedSystems = this.configSystems.map(() => false);

        this.select = this.ngFormBuilder.group({
            all: this.ngFormBuilder.nonNullable.control(false),
            groups: this.ngFormBuilder.nonNullable.array<FormGroup>([]),
            tags: this.ngFormBuilder.nonNullable.array<FormGroup>([]),
            systems: this.ngFormBuilder.nonNullable.array<FormGroup>([]),
        });

        const fcAll = this.select.get('all') as FormControl;
        const faGroups = this.select.get('groups') as FormArray;
        const faSystems = this.select.get('systems') as FormArray;
        const faTags = this.select.get('tags') as FormArray;

        this.configGroups.forEach((configGroup) => {
            const fgGroup = this.ngFormBuilder.group({
                id: this.ngFormBuilder.control(configGroup.get('id')?.value),
                checked: this.ngFormBuilder.control(false),
            });

            faGroups.push(fgGroup);

            fgGroup.valueChanges.pipe(debounceTime(50)).subscribe((vGroup) => {
                faSystems.controls.forEach((fgSystem) => {
                    const faTalkgroups = fgSystem.get('talkgroups') as FormArray;

                    faTalkgroups.controls.forEach((fgTalkgroup) => {
                        if (fgTalkgroup.get('groupIds')?.value.includes(vGroup.id) && fgTalkgroup.get('checked')?.value !== vGroup.checked) {
                            fgTalkgroup.get('checked')?.setValue(vGroup.checked, { emitEvent: false });
                        }
                    });
                });
                
                // Manually trigger indeterminate recalculation after all talkgroups are updated
                this.rebuildSystemIndeterminates();
            });
        });

        this.configSystems.forEach((configSystem, index) => {
            const fcSystemAll = this.ngFormBuilder.control(false);

            const faSystemTalkgroups = this.ngFormBuilder.array<FormGroup<{
                checked: FormControl<boolean>;
                groupIds: FormControl<number[]>;
                id: FormControl<number>;
                tagId: FormControl<number>;
            }>>([]);

            const fgSystem = this.ngFormBuilder.group({
                all: fcSystemAll,
                id: this.ngFormBuilder.control(configSystem.get('systemRef')?.value),
                talkgroups: faSystemTalkgroups
            });

            this.configTalkgroups[index].forEach((configTalkgroup) => {
                const fgSystemTalkgroup = this.ngFormBuilder.group({
                    checked: this.ngFormBuilder.nonNullable.control(false),
                    groupIds: this.ngFormBuilder.nonNullable.control(configTalkgroup.get('groupIds')?.value),
                    id: this.ngFormBuilder.nonNullable.control(configTalkgroup.get('talkgroupRef')?.value),
                    tagId: this.ngFormBuilder.nonNullable.control(configTalkgroup.get('tagId')?.value),
                });

                faSystemTalkgroups.push(fgSystemTalkgroup);

                fgSystemTalkgroup.valueChanges.pipe(debounceTime(50)).subscribe(() => {
                    const vAll = faSystemTalkgroups.controls.every((systemTalkgroup) => systemTalkgroup.get('checked')?.value);

                    fcSystemAll.setValue(vAll, { emitEvent: false });
                });
            });

            faSystems.push(fgSystem);

            fgSystem.valueChanges.pipe(debounceTime(100)).subscribe(() => {
                this.rebuildGroupIndeterminates();
                this.rebuildTagIndeterminates();
            });

            fcSystemAll.valueChanges.pipe(debounceTime(50)).subscribe((vAll) => {
                const faTalkgroups = fgSystem.get('talkgroups') as FormArray;

                faTalkgroups.controls.forEach((fgTalkgroup) => fgTalkgroup.get('checked')?.setValue(vAll, { emitEvent: false }));

                this.rebuildGroupIndeterminates();
                this.rebuildTagIndeterminates();
            });

            faSystemTalkgroups.valueChanges.pipe(debounceTime(100)).subscribe((vSystemTalkgroups) => {
                let off = 0;
                let on = 0;

                vSystemTalkgroups.forEach((vSystemTalkgroup) => {
                    if (vSystemTalkgroup.checked) {
                        on++;

                    } else {
                        off++;
                    }
                });

                this.indeterminate.systems[index] = !!off && !!on;

                faSystems.at(index).get('all')?.setValue(!off && on, { emitEvent: false });
            });
        });

        this.configTags.forEach((configTag) => {
            const fgTag = this.ngFormBuilder.group({
                id: this.ngFormBuilder.control(configTag.value.id),
                checked: this.ngFormBuilder.control(false),
            });

            faTags.push(fgTag);

            fgTag.valueChanges.pipe(debounceTime(50)).subscribe((vTag) => {
                faSystems.controls.forEach((fgSystem) => {
                    const faTalkgroups = fgSystem.get('talkgroups') as FormArray;

                    faTalkgroups.controls.forEach((fgTalkgroup) => {
                        if (fgTalkgroup.value.tagId === vTag.id && fgTalkgroup.value.checked !== vTag.checked) {
                            fgTalkgroup.get('checked')?.setValue(vTag.checked, { emitEvent: false });
                        }
                    });
                });
                
                // Manually trigger indeterminate recalculation after all talkgroups are updated
                this.rebuildSystemIndeterminates();
            });
        });

        fcAll.valueChanges.pipe(debounceTime(50)).subscribe((vAll) => {
            // Update all system-level 'all' checkboxes
            faSystems.controls.forEach((fgSystem) => {
                fgSystem.get('all')?.setValue(vAll, { emitEvent: false });
            });
            
            // Update all individual talkgroups, groups, and tags
            faSystems.controls.flatMap((fgSystem) => {
                const faTalkgroups = fgSystem.get('talkgroups') as FormArray;

                return faTalkgroups.controls;
            }).concat(faGroups.controls, faTags.controls).forEach((control) => {
                control.get('checked')?.setValue(vAll, { emitEvent: false });
            });
        });

        faSystems.valueChanges.pipe(debounceTime(100)).subscribe((vSystems) => {
            let off = 0;
            let on = 0;

            vSystems.forEach((vSystem: System) => {
                if (vSystem.all) {
                    on++;

                } else {
                    off++;
                }
            });

            this.indeterminate.everything = !!off && !!on;

            fcAll.setValue(!off && on, { emitEvent: false });
        });

        const accessValue = (this.access.value || {}) as { systems?: any };
        const scopedSystems = accessValue.systems;

        if (scopedSystems === '*') {
            // When "Everything" is selected, manually check all systems, talkgroups, groups, and tags
            // This ensures they show as checked immediately, even with debouncing
            faSystems.controls.forEach((fgSystem) => {
                fgSystem.get('all')?.setValue(true, { emitEvent: false });
                const faTalkgroups = fgSystem.get('talkgroups') as FormArray;
                faTalkgroups.controls.forEach((fgTalkgroup) => {
                    fgTalkgroup.get('checked')?.setValue(true, { emitEvent: false });
                });
            });
            faGroups.controls.forEach((fgGroup) => {
                fgGroup.get('checked')?.setValue(true, { emitEvent: false });
            });
            faTags.controls.forEach((fgTag) => {
                fgTag.get('checked')?.setValue(true, { emitEvent: false });
            });
            // Set the "Everything" checkbox last
            this.select.get('all')?.setValue(true, { emitEvent: false });

        } else if (Array.isArray(scopedSystems)) {
            scopedSystems.forEach((vSystem: any) => {
                if (typeof vSystem === 'number') {
                    faSystems.controls.find((fgSystem) => fgSystem.get('id')?.value === vSystem)?.get('all')?.setValue(true);

                } else if (vSystem !== null && typeof vSystem === 'object') {
                    const fgSystem = faSystems.controls.find((fg) => {
                        return fg.get('id')?.value === vSystem.id;
                    });

                    if (fgSystem) {
                        if (vSystem.talkgroups === '*') {
                            fgSystem.get('all')?.setValue(true);

                        } else if (Array.isArray(vSystem.talkgroups)) {
                            const faTalkgroups = fgSystem.get('talkgroups') as FormArray;

                            vSystem.talkgroups.forEach((talkgroup: { id: number } | number) => {
                                const talkgroupId = typeof talkgroup === 'number' ? talkgroup : talkgroup.id;

                                const fgTalkgroup = faTalkgroups.controls.find((fg) => fg.get('id')?.value === talkgroupId);

                                fgTalkgroup?.get('checked')?.setValue(true);
                            });

                            fgSystem?.updateValueAndValidity();
                        }
                    }
                }
            });
        }
    }

    accept(): void {
        const access = this.select.get('all')?.value ? '*' : this.select.get('systems')?.value.filter((system: System) => {
            return system['all'] || system['talkgroups'].some((talkgroup: Talkgroup) => talkgroup.checked);
        }).map((system: System) => {
            if (system['all']) {
                return {
                    id: system['id'],
                    talkgroups: '*',
                };

            } else {
                return {
                    id: system['id'],
                    talkgroups: system['talkgroups']
                        .filter((talkgroup: Talkgroup) => talkgroup.checked)
                        .map((talkgroup: Talkgroup) => talkgroup.id),
                };
            }
        });

        this.matDialogRef.close(access);
    }

    cancel(): void {
        this.matDialogRef.close(null);
    }

    private rebuildGroupIndeterminates(): void {
        const faGroups = this.select.get('groups') as FormArray;

        const faSystems = this.select.get('systems') as FormArray;

        faGroups.controls.forEach((fgGroup, index) => {
            let off = 0;
            let on = 0;

            faSystems.controls.forEach((fgSystem) => {
                const faTalkgroups = fgSystem.get('talkgroups') as FormArray;

                faTalkgroups.controls.forEach((fgTalkgroup) => {
                    if (fgTalkgroup.get('groupIds')?.value.includes(fgGroup.get('id')?.value)) {
                        if (fgTalkgroup.get('checked')?.value) {
                            on++;

                        } else {
                            off++;
                        }
                    }
                });
            });

            this.indeterminate.groups[index] = !!off && !!on;

            fgGroup.get('checked')?.setValue(!off && on, { emitEvent: false });
        });
    }

    private rebuildTagIndeterminates(): void {
        const faTags = this.select.get('tags') as FormArray;

        const faSystems = this.select.get('systems') as FormArray;

        faTags.controls.forEach((fgTag, index) => {
            let off = 0;
            let on = 0;

            faSystems.controls.forEach((fgSystem) => {
                const faTalkgroups = fgSystem.get('talkgroups') as FormArray;

                faTalkgroups.controls.forEach((fgTalkgroup) => {
                    if (fgTalkgroup.value.tagId === fgTag.value.id) {
                        if (fgTalkgroup.value.checked) {
                            on++;

                        } else {
                            off++;
                        }
                    }
                });
            });

            this.indeterminate.tags[index] = !!off && !!on;

            fgTag.get('checked')?.setValue(!off && on, { emitEvent: false });
        });
    }

    private rebuildSystemIndeterminates(): void {
        const faSystems = this.select.get('systems') as FormArray;

        faSystems.controls.forEach((fgSystem, index) => {
            const faTalkgroups = fgSystem.get('talkgroups') as FormArray;
            let off = 0;
            let on = 0;

            faTalkgroups.controls.forEach((fgTalkgroup) => {
                if (fgTalkgroup.get('checked')?.value) {
                    on++;
                } else {
                    off++;
                }
            });

            this.indeterminate.systems[index] = !!off && !!on;
            fgSystem.get('all')?.setValue(!off && on, { emitEvent: false });
        });
    }
}
