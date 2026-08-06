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

import { Component, EventEmitter, Input, Output, ChangeDetectionStrategy } from '@angular/core';
import { FormArray, FormGroup } from '@angular/forms';

@Component({
    selector: 'rdio-scanner-admin-systems',
    templateUrl: './systems.component.html',
    styleUrls: ['./systems.component.scss'],
    changeDetection: ChangeDetectionStrategy.OnPush,
    standalone: false
})
export class RdioScannerAdminSystemsComponent {
    @Input() form: FormArray | undefined;

    /** Emitted when the user clicks Add System */
    @Output() addSystem = new EventEmitter<void>();

    get systems(): FormGroup[] {
        if (!this.form) return [];
        return this.form.controls as FormGroup[];
    }
}
