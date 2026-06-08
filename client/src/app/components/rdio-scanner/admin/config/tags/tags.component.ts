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
import { Component, Input } from '@angular/core';
import { FormArray, FormGroup } from '@angular/forms';
import { MatSnackBar } from '@angular/material/snack-bar';
import { RdioScannerAdminService } from '../../admin.service';

@Component({
    selector: 'rdio-scanner-admin-tags',
    templateUrl: './tags.component.html',
    styleUrls: ['./tags.component.scss'],
})
export class RdioScannerAdminTagsComponent {
    @Input() form: FormArray | undefined;
    @Input() originalConfig: any;

    displayedColumns = ['drag', 'color', 'label', 'usage', 'delete'];

    readonly colorOptions = [
        { value: '',        label: 'None (White)',  hex: '#ffffff' },
        { value: '#ff1744', label: 'Red',           hex: '#ff1744' },
        { value: '#ff9100', label: 'Orange',        hex: '#ff9100' },
        { value: '#ffea00', label: 'Yellow',        hex: '#ffea00' },
        { value: '#00e676', label: 'Green',         hex: '#00e676' },
        { value: '#00e5ff', label: 'Cyan',          hex: '#00e5ff' },
        { value: '#2979ff', label: 'Blue',          hex: '#2979ff' },
        { value: '#d500f9', label: 'Magenta',       hex: '#d500f9' },
        { value: '#9e9e9e', label: 'Gray',          hex: '#9e9e9e' },
        { value: '#ffffff', label: 'White',         hex: '#ffffff' },
    ];

    saving = false;

    constructor(
        private adminService: RdioScannerAdminService,
        private snackBar: MatSnackBar,
    ) {}

    get tags(): FormGroup[] {
        if (!this.form) return [];
        return (this.form.controls as FormGroup[])
            .slice()
            .sort((a, b) => (a.value.order || 0) - (b.value.order || 0));
    }

    isTagUnused(tagId: number): boolean {
        if (!this.originalConfig || !this.originalConfig.systems) return false;

        // Check original config data instead of FormArray
        for (const system of this.originalConfig.systems) {
            if (system.talkgroups && Array.isArray(system.talkgroups)) {
                for (const talkgroup of system.talkgroups) {
                    if (talkgroup.tagId === tagId || talkgroup.tag === tagId) {
                        return false;
                    }
                }
            }
        }

        return true;
    }

    getColorLabel(hex: string): string {
        return this.colorOptions.find(c => c.value === hex)?.label ?? (hex || 'None (White)');
    }

    add(): void {
        const tag = this.adminService.newTagForm();
        tag.markAsDirty({ onlySelf: false });
        tag.markAsDirty();
        this.form?.insert(0, tag);
        this.form?.markAsDirty();
    }

    remove(index: number): void {
        this.form?.removeAt(index);
        this.form?.markAsDirty();
        this.saveAll(false);
    }

    drop(event: CdkDragDrop<FormGroup[]>): void {
        if (event.previousIndex === event.currentIndex) return;
        moveItemInArray(event.container.data, event.previousIndex, event.currentIndex);
        event.container.data.forEach((dat, idx) => dat.get('order')?.setValue(idx + 1, { emitEvent: false }));
        this.form?.markAsDirty();
        this.saveAll(false);
    }

    /** Color select changes auto-save. */
    onColorChange(): void {
        this.form?.markAsDirty();
        this.saveAll(false);
    }

    /**
     * API-driven save: PUT /api/admin/tags with the full list. Auto-invoked for
     * structural changes (reorder/remove/color/cleanup); the Save button covers
     * label text edits.
     */
    async saveAll(showToast = true): Promise<void> {
        if (!this.form) return;
        if (this.form.invalid) {
            if (showToast) {
                this.snackBar.open('Fix the highlighted fields before saving.', 'Close', { duration: 4000 });
            }
            return;
        }

        this.saving = true;
        const updated = await this.adminService.saveTags(this.form.getRawValue());
        this.saving = false;

        if (updated) {
            this.form.markAsPristine();
            if (showToast) {
                this.snackBar.open('Tags saved', 'Close', { duration: 1500 });
            }
        } else if (showToast) {
            this.snackBar.open('Failed to save tags. Please try again.', 'Close', { duration: 4000 });
        }
    }

    cleanupUnused(): void {
        if (!this.form || !this.originalConfig?.systems) return;

        const usedTagIds = new Set<number>();
        for (const system of this.originalConfig.systems) {
            if (!system.talkgroups || !Array.isArray(system.talkgroups)) {
                continue;
            }
            for (const talkgroup of system.talkgroups) {
                const tagId = talkgroup.tagId ?? talkgroup.tag;
                if (tagId) {
                    usedTagIds.add(tagId);
                }
            }
        }

        for (let i = this.form.controls.length - 1; i >= 0; i--) {
            const id = this.form.at(i).get('id')?.value;
            if (id && !usedTagIds.has(id)) {
                this.form.removeAt(i);
            }
        }

        this.form.markAsDirty();
        this.saveAll(false);
    }
}
