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

import { Component, EventEmitter, Input, Output, ChangeDetectionStrategy } from '@angular/core';
import { FormArray, FormControl, FormGroup, Validators } from '@angular/forms';

@Component({
    selector: 'rdio-scanner-admin-site',
    templateUrl: './site.component.html',
    changeDetection: ChangeDetectionStrategy.Eager,
    standalone: false
})
export class RdioScannerAdminSiteComponent {
    @Input() form: FormGroup | undefined;

    @Output() remove = new EventEmitter<void>();

    get frequencies(): FormArray {
        return this.form?.get('frequencies') as FormArray;
    }

    addFrequency(): void {
        this.frequencies.push(new FormControl(null, [Validators.required, Validators.min(0)]));
        this.form?.markAsDirty();
    }

    removeFrequency(index: number): void {
        const hz = this.frequencies.at(index)?.value;
        const label = hz != null && hz !== '' ? `${hz} Hz` : 'this frequency';
        if (!confirm(`Are you sure you want to delete ${label}?`)) {
            return;
        }
        this.frequencies.removeAt(index);
        this.form?.markAsDirty();
    }
}
