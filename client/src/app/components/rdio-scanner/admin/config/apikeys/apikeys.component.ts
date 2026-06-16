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
import { ChangeDetectorRef, Component, Input } from '@angular/core';
import { MatDialog } from '@angular/material/dialog';
import { FormArray, FormGroup } from '@angular/forms';
import { MatSnackBar } from '@angular/material/snack-bar';
import { RdioScannerAdminService } from '../../admin.service';
import { RdioScannerAdminSystemsSelectComponent } from '../systems/select/select.component';

@Component({
    selector: 'rdio-scanner-admin-apikeys',
    templateUrl: './apikeys.component.html',
    styleUrls: ['./apikeys.component.scss'],
})
export class RdioScannerAdminApikeysComponent {
    @Input() form: FormArray | undefined;
    @Input() rawSystems: any[] | undefined;

    displayedColumns: string[] = ['drag', 'status', 'ident', 'key', 'access', 'noAudio', 'actions'];

    // Per-row key visibility state
    keyVisible: boolean[] = [];

    saving = false;

    get apikeys(): FormGroup[] {
        // Spread into a new array so mat-table always receives a new reference
        // and correctly detects additions/removals even inside an OnPush parent.
        return [...(this.form?.controls || [])]
            .sort((a, b) => a.value.order - b.value.order) as FormGroup[];
    }

    trackByKey(_index: number, apikey: FormGroup): string {
        return apikey.get('key')?.value ?? String(_index);
    }

    constructor(
        private adminService: RdioScannerAdminService,
        private cdr: ChangeDetectorRef,
        private matDialog: MatDialog,
        private snackBar: MatSnackBar
    ) { }

    add(): void {
        const apikey = this.adminService.newApikeyForm({
            key: this.uuid(),
            systems: '*',
        });

        apikey.markAsDirty({ onlySelf: false });

        this.form?.insert(0, apikey);
        this.keyVisible.unshift(true); // Show new key's value by default

        this.form?.markAsDirty();

        // markForCheck() propagates up through the OnPush parent (config.component)
        // so the new array reference from the getter is picked up and mat-table
        // renders the new row immediately without the user having to click elsewhere.
        this.cdr.markForCheck();
    }

    remove(index: number): void {
        this.form?.removeAt(index);
        this.keyVisible.splice(index, 1);

        this.form?.markAsDirty();
        // Removal is structural — persist immediately.
        this.saveAll(false);
    }

    drop(event: CdkDragDrop<FormGroup[]>): void {
        if (event.previousIndex !== event.currentIndex) {
            moveItemInArray(event.container.data, event.previousIndex, event.currentIndex);

            event.container.data.forEach((dat, idx) => dat.get('order')?.setValue(idx + 1, { emitEvent: false }));

            // Sync visibility array
            const vis = this.keyVisible.splice(event.previousIndex, 1);
            this.keyVisible.splice(event.currentIndex, 0, ...vis);

            this.form?.markAsDirty();
            // Reorder is structural — persist immediately.
            this.saveAll(false);
        }
    }

    select(access: FormGroup): void {
        const matDialogRef = this.matDialog.open(RdioScannerAdminSystemsSelectComponent, {
            data: { access, rawSystems: this.rawSystems },
        });

        matDialogRef.afterClosed().subscribe((data) => {
            if (data) {
                access.get('systems')?.setValue(data);
                access.markAsDirty();
                // Systems-access change — persist immediately.
                this.saveAll(false);
            }
        });
    }

    toggleDisabled(apikey: FormGroup): void {
        const ctrl = apikey.get('disabled');
        if (ctrl) {
            ctrl.setValue(!ctrl.value);
            apikey.markAsDirty();
            // Toggle auto-saves.
            this.saveAll(false);
        }
    }

    onNoAudioSettingChange(apikey: FormGroup): void {
        apikey.markAsDirty();
        this.saveAll(false);
    }

    /**
     * API-driven save: sends the full list to PUT /api/admin/apikeys.
     * Called automatically for structural changes (toggle/reorder/remove/access)
     * and explicitly via the Save button for text edits (ident/key).
     */
    async saveAll(showToast = true): Promise<void> {
        if (!this.form) return;

        // Never persist an invalid list (e.g. a half-entered new row). Only the
        // explicit Save button surfaces the reason to the user.
        if (this.form.invalid) {
            if (showToast) {
                this.snackBar.open('Fix the highlighted fields before saving.', 'Close', { duration: 4000 });
            }
            return;
        }

        this.saving = true;
        const list = this.form.getRawValue();
        const updated = await this.adminService.saveApikeys(list);
        this.saving = false;

        if (updated) {
            this.form.markAsPristine();
            if (showToast) {
                this.snackBar.open('API keys saved', 'Close', { duration: 1500 });
            }
        } else if (showToast) {
            this.snackBar.open('Failed to save API keys. Please try again.', 'Close', { duration: 4000 });
        }
        this.cdr.markForCheck();
    }

    toggleKeyVisible(index: number): void {
        this.keyVisible[index] = !this.keyVisible[index];
    }

    maskKey(key: string): string {
        if (!key) return '—';
        // Show first 8 chars then mask the rest: xxxxxxxx-••••-••••-••••-••••••••••••
        const parts = key.split('-');
        if (parts.length === 5) {
            return `${parts[0]}-••••-••••-••••-••••••••••••`;
        }
        return key.slice(0, 8) + '••••••••••••••••••••••••••••';
    }

    copyKey(key: string): void {
        if (!key) {
            this.snackBar.open('No API key to copy', 'Close', { duration: 3000 });
            return;
        }

        navigator.clipboard.writeText(key).then(() => {
            this.snackBar.open('API key copied to clipboard', 'Close', { duration: 3000 });
        }).catch(() => {
            this.snackBar.open('Failed to copy. Please copy manually.', 'Close', { duration: 5000 });
        });
    }

    private uuid(): string {
        let dt = new Date().getTime();

        return 'xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx'.replace(/[xy]/g, function (c) {
            const r = (dt + Math.random() * 16) % 16 | 0;
            dt = Math.floor(dt / 16);
            return (c === 'x' ? r : (r & 0x3 | 0x8)).toString(16);
        });
    }
}
