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
import { ChangeDetectionStrategy, ChangeDetectorRef, Component, EventEmitter, Input, Output } from '@angular/core';
import { FormArray, FormGroup } from '@angular/forms';
import { MatSnackBar } from '@angular/material/snack-bar';
import { RdioScannerAdminService } from '../../admin.service';

@Component({
    selector: 'rdio-scanner-admin-systems',
    templateUrl: './systems.component.html',
    styleUrls: ['./systems.component.scss'],
    changeDetection: ChangeDetectionStrategy.OnPush,
    standalone: false
})
export class RdioScannerAdminSystemsComponent {
    @Input() form: FormArray | undefined;
    @Input() rawSystems: any[] | undefined;

    @Output() addSystem = new EventEmitter<void>();
    @Output() selectSystem = new EventEmitter<FormGroup>();

    displayedColumns = ['drag', 'label', 'systemRef', 'type', 'talkgroups', 'sites', 'actions'];
    searchQuery = '';
    saving = false;

    constructor(
        private adminService: RdioScannerAdminService,
        private cdr: ChangeDetectorRef,
        private snackBar: MatSnackBar,
    ) { }

    get systems(): FormGroup[] {
        if (!this.form) {
            return [];
        }
        return (this.form.controls as FormGroup[])
            .slice()
            .sort((a, b) => (a.value.order || 0) - (b.value.order || 0)
                || String(a.value.label || '').localeCompare(String(b.value.label || '')));
    }

    get filteredSystems(): FormGroup[] {
        const q = this.searchQuery.trim().toLowerCase();
        if (!q) {
            return this.systems;
        }
        return this.systems.filter((system) => {
            const label = String(system.value.label || '').toLowerCase();
            const ref = String(system.value.systemRef ?? '');
            const type = String(system.value.type || '').toLowerCase();
            return label.includes(q) || ref.includes(q) || type.includes(q);
        });
    }

    onSearch(query: string): void {
        this.searchQuery = query;
        this.cdr.markForCheck();
    }

    talkgroupCount(system: FormGroup): number {
        const raw = this.rawFor(system);
        return raw?.talkgroups?.length ?? system.value.talkgroups?.length ?? 0;
    }

    siteCount(system: FormGroup): number {
        const raw = this.rawFor(system);
        return raw?.sites?.length ?? system.value.sites?.length ?? 0;
    }

    drop(event: CdkDragDrop<FormGroup[]>): void {
        if (this.searchQuery.trim() || event.previousIndex === event.currentIndex) {
            return;
        }
        const all = this.systems;
        if (event.previousIndex < 0 || event.previousIndex >= all.length) {
            return;
        }
        moveItemInArray(all, event.previousIndex, Math.min(event.currentIndex, all.length - 1));
        void this.persistOrder(all);
    }

    sortAlphabetical(): void {
        const all = this.systems.slice().sort((a, b) =>
            String(a.value.label || '').localeCompare(String(b.value.label || ''), undefined, { sensitivity: 'base' }));
        void this.persistOrder(all);
    }

    private async persistOrder(ordered: FormGroup[]): Promise<void> {
        const previousOrders = ordered.map((system) => ({
            system,
            order: system.get('order')?.value,
        }));

        ordered.forEach((system, idx) => system.get('order')?.setValue(idx + 1, { emitEvent: false }));

        const orders = ordered
            .map((system, idx) => ({ id: Number(system.get('id')?.value), order: idx + 1 }))
            .filter((item) => item.id > 0);
        if (!orders.length) {
            this.cdr.markForCheck();
            return;
        }

        this.saving = true;
        this.cdr.markForCheck();
        const saved = await this.adminService.saveSystemsOrder(orders);
        this.saving = false;

        if (saved) {
            // Order-only save: do not markAsPristine. Admin config rebuilds from
            // EmitConfig when the form is pristine, which would wipe unsaved
            // system field edits after a reorder.
            if (this.rawSystems) {
                for (const item of orders) {
                    const raw = this.rawSystems.find((s) => s.id === item.id);
                    if (raw) {
                        raw.order = item.order;
                    }
                }
            }
            this.snackBar.open('System order saved', 'Close', { duration: 1500 });
        } else {
            for (const item of previousOrders) {
                item.system.get('order')?.setValue(item.order, { emitEvent: false });
            }
            this.snackBar.open('Failed to save system order. Please try again.', 'Close', { duration: 4000 });
        }
        this.cdr.markForCheck();
    }

    private rawFor(system: FormGroup): any | undefined {
        const id = system.value.id;
        if (!id || !this.rawSystems) {
            return undefined;
        }
        return this.rawSystems.find((s) => s.id === id);
    }
}
