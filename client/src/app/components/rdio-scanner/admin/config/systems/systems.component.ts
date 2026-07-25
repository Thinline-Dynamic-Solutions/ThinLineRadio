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

import { Component, EventEmitter, Input, Output, ChangeDetectionStrategy, ChangeDetectorRef } from '@angular/core';
import { CdkDragDrop, moveItemInArray } from '@angular/cdk/drag-drop';
import { FormArray, FormGroup } from '@angular/forms';
import { MatSnackBar } from '@angular/material/snack-bar';
import { RdioScannerAdminService } from '../../admin.service';

@Component({
    selector: 'rdio-scanner-admin-systems',
    templateUrl: './systems.component.html',
    styleUrls: ['./systems.component.scss'],
    changeDetection: ChangeDetectionStrategy.OnPush
})
export class RdioScannerAdminSystemsComponent {
    @Input() form: FormArray | undefined;

    /** Emitted when the user clicks to open a specific system */
    @Output() systemSelected = new EventEmitter<FormGroup>();

    /** Emitted when the user clicks Add System */
    @Output() addSystem = new EventEmitter<void>();

    displayedColumns: string[] = ['drag', 'systemRef', 'label', 'type', 'talkgroups', 'sites', 'alertsEnabled', 'actions'];

    // Search
    systemsSearchTerm: string = '';
    saving = false;

    constructor(
        private adminService: RdioScannerAdminService,
        private cdr: ChangeDetectorRef,
        private snackBar: MatSnackBar,
    ) { }

    get systems(): FormGroup[] {
        if (!this.form) return [];
        return (this.form.controls as FormGroup[])
            .slice()
            .sort((a, b) => (a.value.order || 0) - (b.value.order || 0));
    }

    get filteredSystems(): FormGroup[] {
        let filtered = this.systems;
        if (this.systemsSearchTerm.trim()) {
            const search = this.systemsSearchTerm.toLowerCase();
            filtered = filtered.filter(sys => {
                const label = (sys.value.label || '').toLowerCase();
                const id = (sys.value.systemRef || '').toString();
                return label.includes(search) || id.includes(search);
            });
        }
        return filtered;
    }

    getTalkgroupCount(system: FormGroup): number {
        const talkgroups = system.get('talkgroups');
        return talkgroups ? (talkgroups as any).length : 0;
    }

    getSiteCount(system: FormGroup): number {
        const sites = system.get('sites');
        return sites ? (sites as any).length : 0;
    }

    removeAll(): void {
        if (!this.form || this.form.length === 0) return;

        const count = this.form.length;
        if (!confirm(`Are you sure you want to delete all ${count} system${count > 1 ? 's' : ''}? This cannot be undone.`)) {
            return;
        }

        while (this.form.length > 0) {
            this.form.removeAt(0);
        }
        this.form.markAsDirty();
        this.cdr.markForCheck();
    }

    onSystemsSearchChange(searchTerm: string): void {
        this.systemsSearchTerm = searchTerm;
        this.cdr.markForCheck();
    }

    dropSystem(event: CdkDragDrop<FormGroup[]>): void {
        if (event.previousIndex === event.currentIndex) return;
        if (!this.form) return;
        // Move within the displayed (sorted) array and update each system's order field
        moveItemInArray(event.container.data, event.previousIndex, event.currentIndex);
        event.container.data.forEach((sys, idx) => {
            const orderCtrl = sys.get('order');
            orderCtrl?.setValue(idx + 1, { emitEvent: false });
            orderCtrl?.markAsDirty();
        });
        this.form.markAsDirty();
        this.cdr.markForCheck();
        // Same pattern as Tags: persist reorder immediately.
        void this.saveOrder(false);
    }

    /**
     * Persist current system order via PUT /api/admin/systems/order.
     * Auto-invoked on drag-reorder; Save button covers retries / explicit save.
     */
    async saveOrder(showToast = true): Promise<void> {
        if (!this.form || this.saving) return;

        const orders = (this.form.controls as FormGroup[])
            .map((sys) => ({
                id: Number(sys.get('id')?.value),
                order: Number(sys.get('order')?.value) || 0,
            }))
            .filter((row) => Number.isFinite(row.id) && row.id > 0);

        if (orders.length === 0) {
            if (showToast) {
                this.snackBar.open('Save each new system first, then reorder.', 'Close', { duration: 4000 });
            }
            return;
        }

        this.saving = true;
        this.cdr.markForCheck();
        const updated = await this.adminService.saveSystemsOrder(orders);
        this.saving = false;

        if (updated) {
            // Sync order values from server response when present.
            for (const serverSys of updated) {
                const id = serverSys?.id;
                if (!id) continue;
                const local = (this.form.controls as FormGroup[]).find(
                    (c) => Number(c.get('id')?.value) === Number(id),
                );
                if (local && serverSys.order != null) {
                    local.get('order')?.setValue(serverSys.order, { emitEvent: false });
                }
            }
            // onlySelf: don't clear dirty state on unrelated config sections.
            for (const sys of this.form.controls as FormGroup[]) {
                sys.get('order')?.markAsPristine({ onlySelf: true });
            }
            this.form.markAsPristine({ onlySelf: true });
            if (showToast) {
                this.snackBar.open('System order saved', 'Close', { duration: 2000 });
            }
        } else if (showToast) {
            this.snackBar.open('Failed to save system order.', 'Close', { duration: 4000 });
        }
        this.cdr.markForCheck();
    }
}
