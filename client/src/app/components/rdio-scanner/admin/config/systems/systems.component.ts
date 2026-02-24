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

import { Component, EventEmitter, Input, Output } from '@angular/core';
import { CdkDragDrop, moveItemInArray } from '@angular/cdk/drag-drop';
import { FormArray, FormGroup } from '@angular/forms';

@Component({
    selector: 'rdio-scanner-admin-systems',
    templateUrl: './systems.component.html',
    styleUrls: ['./systems.component.scss'],
})
export class RdioScannerAdminSystemsComponent {
    @Input() form: FormArray | undefined;

    /** Emitted when the user clicks to open a specific system */
    @Output() systemSelected = new EventEmitter<FormGroup>();

    /** Emitted when the user clicks Add System */
    @Output() addSystem = new EventEmitter<void>();

    displayedColumns: string[] = ['drag', 'systemRef', 'label', 'type', 'talkgroups', 'sites', 'actions'];

    // Search
    systemsSearchTerm: string = '';

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
        const talkgroups = system.get('talkgroups') as FormArray;
        return talkgroups ? talkgroups.length : 0;
    }

    getSiteCount(system: FormGroup): number {
        const sites = system.get('sites') as FormArray;
        return sites ? sites.length : 0;
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
    }

    onSystemsSearchChange(searchTerm: string): void {
        this.systemsSearchTerm = searchTerm;
    }

    dropSystem(event: CdkDragDrop<FormGroup[]>): void {
        if (event.previousIndex === event.currentIndex) return;
        if (!this.form) return;
        // Move within the displayed (sorted) array and update each system's order field
        moveItemInArray(event.container.data, event.previousIndex, event.currentIndex);
        event.container.data.forEach((sys, idx) =>
            sys.get('order')?.setValue(idx + 1, { emitEvent: false })
        );
        this.form.markAsDirty();
    }
}
