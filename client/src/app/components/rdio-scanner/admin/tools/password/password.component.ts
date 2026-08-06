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

import { Component, OnInit, OnDestroy, ChangeDetectionStrategy } from '@angular/core';
import { AbstractControl, FormBuilder, FormGroup, ValidatorFn, ValidationErrors, Validators } from '@angular/forms';
import { MatSnackBar, MatSnackBarConfig } from '@angular/material/snack-bar';
import { RdioScannerAdminService } from '../../admin.service';
import { Subscription } from 'rxjs';

@Component({
    selector: 'rdio-scanner-admin-password',
    styleUrls: ['./password.component.scss'],
    templateUrl: './password.component.html',
    changeDetection: ChangeDetectionStrategy.Default,
    standalone: false
})
export class RdioScannerAdminPasswordComponent implements OnInit, OnDestroy {
    form: FormGroup;
    adminLocalhostOnly = false;
    adminPasswordLoginDisabled = false;
    adminAllowedIPs = '';
    private eventSubscription: Subscription | undefined;

    constructor(
        private adminService: RdioScannerAdminService,
        private matSnackBar: MatSnackBar,
        private ngFormBuilder: FormBuilder,
    ) {
        this.form = this.ngFormBuilder.group({
            currentPassword: [null, Validators.required],
            newPassword: [null, [Validators.required, Validators.minLength(8)]],
            verifyNewPassword: [null, [Validators.required, this.validatePassword()]],
        });
    }

    ngOnInit(): void {
        this.loadConfig();
        
        this.eventSubscription = this.adminService.event.subscribe(async (event) => {
            if (event.config || event.authenticated) {
                await this.loadConfig();
            }
        });
    }

    ngOnDestroy(): void {
        this.eventSubscription?.unsubscribe();
    }

    private async loadConfig(): Promise<void> {
        try {
            const config = await this.adminService.getConfig();
            if (config?.options) {
                this.adminLocalhostOnly = config.options.adminLocalhostOnly || false;
                this.adminPasswordLoginDisabled = config.options.adminPasswordLoginDisabled || false;
                this.adminAllowedIPs = config.options.adminAllowedIPs || '';
            }
        } catch (error) {
            // Silently fail if not authenticated yet
        }
    }

    private async patchOptions(patch: Record<string, any>): Promise<void> {
        const currentConfig = await this.adminService.getConfig();
        if (currentConfig?.options) {
            Object.assign(currentConfig.options, patch);
            await this.adminService.saveConfig(currentConfig);
        }
    }

    async toggleAdminLocalhost(enabled: boolean): Promise<void> {
        const snackConfig: MatSnackBarConfig = { duration: 3000 };
        try {
            await this.patchOptions({ adminLocalhostOnly: enabled });
            this.adminLocalhostOnly = enabled;
            this.matSnackBar.open(enabled ? 'Admin restricted to localhost' : 'Localhost restriction removed', '', snackConfig);
        } catch {
            this.matSnackBar.open('Failed to update setting', '', snackConfig);
            this.adminLocalhostOnly = !enabled;
        }
    }

    async toggleAdminPasswordDisabled(enabled: boolean): Promise<void> {
        const snackConfig: MatSnackBarConfig = { duration: 3000 };
        try {
            await this.patchOptions({ adminPasswordLoginDisabled: enabled });
            this.adminPasswordLoginDisabled = enabled;
            this.matSnackBar.open(
                enabled ? 'Admin password login disabled' : 'Admin password login re-enabled',
                '', snackConfig
            );
        } catch {
            this.matSnackBar.open('Failed to update setting', '', snackConfig);
            this.adminPasswordLoginDisabled = !enabled;
        }
    }

    async saveAllowedIPs(): Promise<void> {
        const snackConfig: MatSnackBarConfig = { duration: 3000 };
        try {
            await this.patchOptions({ adminAllowedIPs: this.adminAllowedIPs });
            this.matSnackBar.open('IP allow list saved', '', snackConfig);
        } catch {
            this.matSnackBar.open('Failed to save IP allow list', '', snackConfig);
        }
    }

    private validatePassword(): ValidatorFn {
        return (control: AbstractControl): ValidationErrors | null => {
            return typeof control.value === 'string' && control.value.length
                ? control.value === control.parent?.value.newPassword
                    ? null : { invalid: true } : null;
        }
    }

    reset(): void {
        this.form.reset();
    }

    async save(): Promise<void> {
        const config: MatSnackBarConfig = { duration: 5000 };
        const currentPassword = this.form.get('currentPassword')?.value;
        const newPassword = this.form.get('newPassword')?.value;

        this.form.disable();

        try {
            if (currentPassword && newPassword) {
                await this.adminService.changePassword(currentPassword, newPassword);
            }

            this.matSnackBar.open('Password changed successfully', '', config);

            this.form.reset();
        } catch (_) {
            this.matSnackBar.open('Unable to change password', '', config);
        }

        this.form.enable();
    }
}
