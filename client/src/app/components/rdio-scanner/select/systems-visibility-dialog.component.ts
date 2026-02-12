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

import { Component, Inject } from '@angular/core';
import { MAT_DIALOG_DATA, MatDialogRef } from '@angular/material/dialog';

export interface SystemVisibilityItem {
    id: number;
    label: string;
    hidden: boolean;
}

@Component({
    selector: 'rdio-scanner-systems-visibility-dialog',
    templateUrl: './systems-visibility-dialog.component.html',
    styleUrls: ['./systems-visibility-dialog.component.scss'],
})
export class SystemsVisibilityDialogComponent {
    systems: SystemVisibilityItem[];

    constructor(
        public dialogRef: MatDialogRef<SystemsVisibilityDialogComponent>,
        @Inject(MAT_DIALOG_DATA) public data: { systems: SystemVisibilityItem[] },
    ) {
        // Create a copy of the systems array so we can modify it
        this.systems = data.systems.map(s => ({ ...s }));
    }

    toggleSystem(system: SystemVisibilityItem): void {
        system.hidden = !system.hidden;
    }

    save(): void {
        // Return the updated systems with their visibility state
        this.dialogRef.close(this.systems.map(s => ({
            systemId: s.id,
            hidden: s.hidden,
        })));
    }

    cancel(): void {
        this.dialogRef.close();
    }
}
