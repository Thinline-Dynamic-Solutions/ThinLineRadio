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
import { ChangeDetectorRef, Component, Input, ChangeDetectionStrategy } from '@angular/core';
import { MatDialog } from '@angular/material/dialog';
import { FormArray, FormGroup } from '@angular/forms';
import { PageEvent } from '@angular/material/paginator';
import { MatSnackBar } from '@angular/material/snack-bar';
import { RdioScannerAdminService } from '../../admin.service';
import { RdioScannerAdminSystemsSelectComponent, SYSTEMS_SELECT_DIALOG_OPTIONS } from '../systems/select/select.component';

@Component({
    selector: 'rdio-scanner-admin-apikeys',
    templateUrl: './apikeys.component.html',
    styleUrls: ['./apikeys.component.scss'],
    changeDetection: ChangeDetectionStrategy.Eager,
    standalone: false
})
export class RdioScannerAdminApikeysComponent {
    @Input() form: FormArray | undefined;
    @Input() rawSystems: any[] | undefined;

    displayedColumns: string[] = ['drag', 'status', 'ident', 'key', 'access', 'systems', 'alerts', 'time', 'actions'];

    readonly pageSize = 10;
    pageIndex = 0;
    searchQuery = '';

    /** Per-key visibility keyed by the key string (stable across pagination). */
    private keyVisibleMap: Record<string, boolean> = {};

    saving = false;

    get apikeys(): FormGroup[] {
        return [...(this.form?.controls || [])]
            .sort((a, b) => (a.value.order || 0) - (b.value.order || 0)) as FormGroup[];
    }

    get filteredApikeys(): FormGroup[] {
        const q = this.searchQuery.trim().toLowerCase();
        if (!q) {
            return this.apikeys;
        }
        return this.apikeys.filter((apikey) => {
            const ident = String(apikey.value.ident || '').toLowerCase();
            const key = String(apikey.value.key || '').toLowerCase();
            return ident.includes(q) || key.includes(q);
        });
    }

    get pagedApikeys(): FormGroup[] {
        const start = this.pageIndex * this.pageSize;
        return this.filteredApikeys.slice(start, start + this.pageSize);
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

    onSearch(value: string): void {
        this.searchQuery = value ?? '';
        this.pageIndex = 0;
        this.cdr.markForCheck();
    }

    onPage(event: PageEvent): void {
        this.pageIndex = event.pageIndex;
        this.cdr.markForCheck();
    }

    private clampPage(): void {
        const maxPage = Math.max(0, Math.ceil(this.filteredApikeys.length / this.pageSize) - 1);
        if (this.pageIndex > maxPage) {
            this.pageIndex = maxPage;
        }
    }

    add(): void {
        const key = this.uuid();
        const apikey = this.adminService.newApikeyForm({
            key,
            systems: '*',
        });

        apikey.markAsDirty({ onlySelf: false });

        this.form?.insert(0, apikey);
        this.keyVisibleMap[key] = true;

        this.form?.markAsDirty();
        this.pageIndex = 0;
        this.cdr.markForCheck();
    }

    remove(apikey: FormGroup): void {
        const index = this.form?.controls.indexOf(apikey) ?? -1;
        if (index < 0) {
            return;
        }
        const ident = (apikey.get('ident')?.value || '').toString().trim() || 'this API key';
        if (!confirm(`Are you sure you want to delete API key "${ident}"?\n\nRecorders using this key will stop uploading.`)) {
            return;
        }
        const key = String(apikey.get('key')?.value || '');
        this.form?.removeAt(index);
        if (key) {
            delete this.keyVisibleMap[key];
        }

        this.form?.markAsDirty();
        this.clampPage();
        this.saveAll(false);
    }

    drop(event: CdkDragDrop<FormGroup[]>): void {
        if (this.searchQuery.trim() || event.previousIndex === event.currentIndex) {
            return;
        }
        const all = this.apikeys;
        const from = this.pageIndex * this.pageSize + event.previousIndex;
        const to = this.pageIndex * this.pageSize + event.currentIndex;
        if (from === to || from < 0 || from >= all.length) {
            return;
        }
        moveItemInArray(all, from, Math.min(to, all.length - 1));
        all.forEach((dat, idx) => dat.get('order')?.setValue(idx + 1, { emitEvent: false }));

        this.form?.markAsDirty();
        this.saveAll(false);
    }

    select(access: FormGroup): void {
        const matDialogRef = this.matDialog.open(RdioScannerAdminSystemsSelectComponent, {
            ...SYSTEMS_SELECT_DIALOG_OPTIONS,
            data: { access, rawSystems: this.rawSystems },
        });

        matDialogRef.afterClosed().subscribe((data) => {
            if (data) {
                access.get('systems')?.setValue(data);
                access.markAsDirty();
                this.saveAll(false);
            }
        });
    }

    toggleDisabled(apikey: FormGroup): void {
        const ctrl = apikey.get('disabled');
        if (ctrl) {
            ctrl.setValue(!ctrl.value);
            apikey.markAsDirty();
            this.saveAll(false);
        }
    }

    onNoAudioSettingChange(apikey: FormGroup): void {
        apikey.markAsDirty();
        this.saveAll(false);
    }

    async saveAll(showToast = true): Promise<void> {
        if (!this.form) return;

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

    isKeyVisible(apikey: FormGroup): boolean {
        const key = String(apikey.get('key')?.value || '');
        return !!this.keyVisibleMap[key];
    }

    toggleKeyVisible(apikey: FormGroup): void {
        const key = String(apikey.get('key')?.value || '');
        if (!key) {
            return;
        }
        this.keyVisibleMap[key] = !this.keyVisibleMap[key];
        this.cdr.markForCheck();
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
