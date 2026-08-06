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

import { ChangeDetectionStrategy, ChangeDetectorRef, Component, Inject, ViewEncapsulation } from '@angular/core';
import { FormArray, FormBuilder, FormControl, FormGroup } from '@angular/forms';
import { MAT_DIALOG_DATA, MatDialogConfig, MatDialogRef } from '@angular/material/dialog';

interface System {
    all: boolean;
    id: number;
    talkgroups: Talkgroup[];
}

interface Talkgroup {
    checked: boolean;
    id: number;
}

/** Shared MatDialog options for systems/talkgroups selection. */
export const SYSTEMS_SELECT_DIALOG_OPTIONS: MatDialogConfig = {
    width: '100vw',
    height: '100vh',
    maxWidth: '100vw',
    maxHeight: '100vh',
    panelClass: ['admin-dialog-panel', 'systems-select-dialog'],
    backdropClass: 'admin-dialog-backdrop',
};

@Component({
    changeDetection: ChangeDetectionStrategy.OnPush,
    encapsulation: ViewEncapsulation.None,
    selector: 'rdio-scanner-admin-systems-selection',
    styleUrls: ['./select.component.scss'],
    templateUrl: './select.component.html',
    standalone: false
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
    expandedSystems: boolean[] = [];

    /** Prevents cascading checkbox handlers from fighting each other. */
    private syncing = false;

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
        this.cdr.markForCheck();
    }

    expandAll(): void {
        this.expandedSystems = this.configSystems.map(() => true);
        this.cdr.markForCheck();
    }

    collapseAll(): void {
        this.expandedSystems = this.configSystems.map(() => false);
        this.cdr.markForCheck();
    }

    get configGroups(): FormGroup[] {
        const faGroups = this.access.root.get('groups') as FormArray;
        return (faGroups?.controls || []) as FormGroup[];
    }

    get configSystems(): FormGroup[] {
        const faSystems = this.access.root.get('systems') as FormArray;
        return (faSystems?.controls || []) as FormGroup[];
    }

    get configTags(): FormGroup[] {
        const faTags = this.access.root.get('tags') as FormArray;
        return (faTags?.controls || []) as FormGroup[];
    }

    constructor(
        @Inject(MAT_DIALOG_DATA) dialogData: { access: FormGroup; rawSystems?: any[] } | FormGroup,
        private matDialogRef: MatDialogRef<RdioScannerAdminSystemsSelectComponent>,
        private ngFormBuilder: FormBuilder,
        private cdr: ChangeDetectorRef,
    ) {
        const rawSystems: any[] | undefined = (dialogData as any)?.rawSystems;
        this.access = (dialogData as any)?.access instanceof FormGroup
            ? (dialogData as any).access
            : dialogData as FormGroup;

        this.configTalkgroups = this.configSystems.map((fgSystem) => {
            const faTalkgroups = fgSystem.get('talkgroups') as FormArray;

            if ((faTalkgroups?.length ?? 0) > 0) {
                return faTalkgroups.controls as FormGroup[];
            }

            if (rawSystems) {
                const systemRef = fgSystem.get('systemRef')?.value;
                const rawSystem = rawSystems.find((s: any) => s.systemRef === systemRef);
                if (rawSystem?.talkgroups?.length) {
                    return (rawSystem.talkgroups as any[]).map((tg: any) =>
                        this.ngFormBuilder.group({
                            groupIds: this.ngFormBuilder.control(this.normalizeIds(tg.groupIds || tg.group || [])),
                            label: this.ngFormBuilder.control(tg.label || ''),
                            talkgroupRef: this.ngFormBuilder.control(tg.talkgroupRef),
                            tagId: this.ngFormBuilder.control(tg.tagId ?? tg.tag ?? null),
                        })
                    );
                }
            }

            return [];
        });

        this.expandedSystems = this.configSystems.map(() => false);

        this.select = this.ngFormBuilder.group({
            all: this.ngFormBuilder.nonNullable.control(false),
            groups: this.ngFormBuilder.nonNullable.array<FormGroup>([]),
            tags: this.ngFormBuilder.nonNullable.array<FormGroup>([]),
            systems: this.ngFormBuilder.nonNullable.array<FormGroup>([]),
        });

        const faGroups = this.select.get('groups') as FormArray;
        const faSystems = this.select.get('systems') as FormArray;
        const faTags = this.select.get('tags') as FormArray;

        this.configGroups.forEach((configGroup) => {
            faGroups.push(this.ngFormBuilder.group({
                id: this.ngFormBuilder.control(Number(configGroup.get('id')?.value)),
                checked: this.ngFormBuilder.control(false),
            }));
            this.indeterminate.groups.push(false);
        });

        this.configSystems.forEach((configSystem, index) => {
            const faSystemTalkgroups = this.ngFormBuilder.array<FormGroup>([]);

            const fgSystem = this.ngFormBuilder.group({
                all: this.ngFormBuilder.control(false),
                id: this.ngFormBuilder.control(configSystem.get('systemRef')?.value),
                talkgroups: faSystemTalkgroups,
            });

            this.configTalkgroups[index].forEach((configTalkgroup) => {
                faSystemTalkgroups.push(this.ngFormBuilder.group({
                    checked: this.ngFormBuilder.nonNullable.control(false),
                    groupIds: this.ngFormBuilder.nonNullable.control(
                        this.normalizeIds(configTalkgroup.get('groupIds')?.value || configTalkgroup.get('group')?.value || [])
                    ),
                    id: this.ngFormBuilder.nonNullable.control(configTalkgroup.get('talkgroupRef')?.value),
                    tagId: this.ngFormBuilder.nonNullable.control(
                        this.toNumOrNull(configTalkgroup.get('tagId')?.value ?? configTalkgroup.get('tag')?.value)
                    ),
                }));
            });

            faSystems.push(fgSystem);
            this.indeterminate.systems.push(false);
        });

        this.configTags.forEach((configTag) => {
            faTags.push(this.ngFormBuilder.group({
                id: this.ngFormBuilder.control(Number(configTag.value.id)),
                checked: this.ngFormBuilder.control(false),
            }));
            this.indeterminate.tags.push(false);
        });

        this.applySavedSelection();
        this.syncDerivedState();
    }

    onEverythingChange(checked: boolean): void {
        if (this.syncing) {
            return;
        }
        this.syncing = true;
        this.forEachTalkgroup((fg) => fg.get('checked')?.setValue(checked, { emitEvent: false }));
        this.syncDerivedState();
        this.syncing = false;
        this.cdr.markForCheck();
    }

    onGroupChange(groupIndex: number, checked: boolean): void {
        if (this.syncing) {
            return;
        }
        const groupId = Number((this.select.get('groups') as FormArray).at(groupIndex).get('id')?.value);
        this.syncing = true;
        this.forEachTalkgroup((fg) => {
            const ids = this.normalizeIds(fg.get('groupIds')?.value);
            if (ids.includes(groupId)) {
                fg.get('checked')?.setValue(checked, { emitEvent: false });
            }
        });
        this.syncDerivedState();
        this.syncing = false;
        this.cdr.markForCheck();
    }

    onTagChange(tagIndex: number, checked: boolean): void {
        if (this.syncing) {
            return;
        }
        const tagId = Number((this.select.get('tags') as FormArray).at(tagIndex).get('id')?.value);
        this.syncing = true;
        this.forEachTalkgroup((fg) => {
            if (Number(fg.get('tagId')?.value) === tagId) {
                fg.get('checked')?.setValue(checked, { emitEvent: false });
            }
        });
        this.syncDerivedState();
        this.syncing = false;
        this.cdr.markForCheck();
    }

    onSystemChange(systemIndex: number, checked: boolean): void {
        if (this.syncing) {
            return;
        }
        this.syncing = true;
        const faTalkgroups = (this.select.get('systems') as FormArray).at(systemIndex).get('talkgroups') as FormArray;
        faTalkgroups.controls.forEach((fg) => fg.get('checked')?.setValue(checked, { emitEvent: false }));
        this.syncDerivedState();
        this.syncing = false;
        this.cdr.markForCheck();
    }

    onTalkgroupChange(systemIndex: number, talkgroupIndex: number, checked: boolean): void {
        if (this.syncing) {
            return;
        }
        this.syncing = true;
        const faTalkgroups = (this.select.get('systems') as FormArray).at(systemIndex).get('talkgroups') as FormArray;
        faTalkgroups.at(talkgroupIndex).get('checked')?.setValue(checked, { emitEvent: false });
        this.syncDerivedState();
        this.syncing = false;
        this.cdr.markForCheck();
    }

    isEverythingChecked(): boolean {
        return !!this.select.get('all')?.value;
    }

    isGroupChecked(index: number): boolean {
        return !!((this.select.get('groups') as FormArray).at(index)?.get('checked')?.value);
    }

    isTagChecked(index: number): boolean {
        return !!((this.select.get('tags') as FormArray).at(index)?.get('checked')?.value);
    }

    isSystemChecked(index: number): boolean {
        return !!((this.select.get('systems') as FormArray).at(index)?.get('all')?.value);
    }

    isTalkgroupChecked(systemIndex: number, talkgroupIndex: number): boolean {
        const fa = (this.select.get('systems') as FormArray).at(systemIndex)?.get('talkgroups') as FormArray;
        return !!fa?.at(talkgroupIndex)?.get('checked')?.value;
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
            }
            return {
                id: system['id'],
                talkgroups: system['talkgroups']
                    .filter((talkgroup: Talkgroup) => talkgroup.checked)
                    .map((talkgroup: Talkgroup) => talkgroup.id),
            };
        });

        this.matDialogRef.close(access);
    }

    cancel(): void {
        this.matDialogRef.close(null);
    }

    private applySavedSelection(): void {
        const accessValue = (this.access.value || {}) as { systems?: any };
        const scopedSystems = accessValue.systems;
        const faSystems = this.select.get('systems') as FormArray;

        if (scopedSystems === '*') {
            this.forEachTalkgroup((fg) => fg.get('checked')?.setValue(true, { emitEvent: false }));
            return;
        }

        if (!Array.isArray(scopedSystems)) {
            return;
        }

        scopedSystems.forEach((vSystem: any) => {
            if (typeof vSystem === 'number') {
                const fgSystem = faSystems.controls.find((fg) => fg.get('id')?.value === vSystem);
                const faTalkgroups = fgSystem?.get('talkgroups') as FormArray | undefined;
                faTalkgroups?.controls.forEach((fg) => fg.get('checked')?.setValue(true, { emitEvent: false }));
                return;
            }

            if (vSystem !== null && typeof vSystem === 'object') {
                const fgSystem = faSystems.controls.find((fg) => fg.get('id')?.value === vSystem.id);
                if (!fgSystem) {
                    return;
                }
                const faTalkgroups = fgSystem.get('talkgroups') as FormArray;
                if (vSystem.talkgroups === '*') {
                    faTalkgroups.controls.forEach((fg) => fg.get('checked')?.setValue(true, { emitEvent: false }));
                } else if (Array.isArray(vSystem.talkgroups)) {
                    vSystem.talkgroups.forEach((talkgroup: { id: number } | number) => {
                        const talkgroupId = typeof talkgroup === 'number' ? talkgroup : talkgroup.id;
                        const fgTalkgroup = faTalkgroups.controls.find((fg) => fg.get('id')?.value === talkgroupId);
                        fgTalkgroup?.get('checked')?.setValue(true, { emitEvent: false });
                    });
                }
            }
        });
    }

    /**
     * Derive Everything / system / group / tag checkbox + indeterminate state
     * ONLY from talkgroup checked flags (single source of truth).
     */
    private syncDerivedState(): void {
        const faGroups = this.select.get('groups') as FormArray;
        const faSystems = this.select.get('systems') as FormArray;
        const faTags = this.select.get('tags') as FormArray;
        const fcAll = this.select.get('all') as FormControl;

        let systemsOn = 0;
        let systemsOff = 0;

        faSystems.controls.forEach((fgSystem, systemIndex) => {
            const faTalkgroups = fgSystem.get('talkgroups') as FormArray;
            let on = 0;
            let off = 0;
            faTalkgroups.controls.forEach((fg) => {
                if (fg.get('checked')?.value) {
                    on++;
                } else {
                    off++;
                }
            });

            const allOn = on > 0 && off === 0;
            const noneOn = on === 0;
            this.indeterminate.systems[systemIndex] = on > 0 && off > 0;
            fgSystem.get('all')?.setValue(allOn, { emitEvent: false });

            if (faTalkgroups.length === 0) {
                // Empty systems count as off for Everything.
                systemsOff++;
            } else if (allOn) {
                systemsOn++;
            } else if (noneOn) {
                systemsOff++;
            } else {
                systemsOn++;
                systemsOff++;
            }
        });

        this.indeterminate.everything = systemsOn > 0 && systemsOff > 0;
        fcAll.setValue(systemsOn > 0 && systemsOff === 0 && faSystems.length > 0, { emitEvent: false });

        faGroups.controls.forEach((fgGroup, index) => {
            const groupId = Number(fgGroup.get('id')?.value);
            let on = 0;
            let off = 0;
            this.forEachTalkgroup((fg) => {
                const ids = this.normalizeIds(fg.get('groupIds')?.value);
                if (!ids.includes(groupId)) {
                    return;
                }
                if (fg.get('checked')?.value) {
                    on++;
                } else {
                    off++;
                }
            });
            this.indeterminate.groups[index] = on > 0 && off > 0;
            fgGroup.get('checked')?.setValue(on > 0 && off === 0, { emitEvent: false });
        });

        faTags.controls.forEach((fgTag, index) => {
            const tagId = Number(fgTag.get('id')?.value);
            let on = 0;
            let off = 0;
            this.forEachTalkgroup((fg) => {
                if (Number(fg.get('tagId')?.value) !== tagId) {
                    return;
                }
                if (fg.get('checked')?.value) {
                    on++;
                } else {
                    off++;
                }
            });
            this.indeterminate.tags[index] = on > 0 && off > 0;
            fgTag.get('checked')?.setValue(on > 0 && off === 0, { emitEvent: false });
        });
    }

    private forEachTalkgroup(fn: (fg: FormGroup) => void): void {
        const faSystems = this.select.get('systems') as FormArray;
        faSystems.controls.forEach((fgSystem) => {
            const faTalkgroups = fgSystem.get('talkgroups') as FormArray;
            faTalkgroups.controls.forEach((fg) => fn(fg as FormGroup));
        });
    }

    private normalizeIds(value: unknown): number[] {
        if (!Array.isArray(value)) {
            if (value == null || value === '') {
                return [];
            }
            const n = Number(value);
            return Number.isFinite(n) ? [n] : [];
        }
        return value
            .map((v) => Number(v))
            .filter((n) => Number.isFinite(n));
    }

    private toNumOrNull(value: unknown): number | null {
        if (value == null || value === '') {
            return null;
        }
        const n = Number(value);
        return Number.isFinite(n) ? n : null;
    }
}
