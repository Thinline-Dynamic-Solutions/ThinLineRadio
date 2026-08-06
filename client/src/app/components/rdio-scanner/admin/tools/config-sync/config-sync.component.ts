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

import { Component, OnInit, OnDestroy } from '@angular/core';
import { AbstractControl, FormBuilder, FormGroup, ValidatorFn, ValidationErrors, Validators } from '@angular/forms';
import { MatSnackBar, MatSnackBarConfig } from '@angular/material/snack-bar';
import { RdioScannerAdminService } from '../../admin.service';
import { Subscription } from 'rxjs';

@Component({
    selector: 'rdio-scanner-admin-config-sync',
    styleUrls: ['./config-sync.component.scss'],
    templateUrl: './config-sync.component.html',
    standalone: false
})
export class RdioScannerAdminConfigSyncComponent implements OnInit, OnDestroy {
    form: FormGroup;
    configSyncEnabled = false;
    configSyncPath = '';
    private eventSubscription: Subscription | undefined;

    constructor(
        private adminService: RdioScannerAdminService,
        private matSnackBar: MatSnackBar,
        private ngFormBuilder: FormBuilder,
    ) {
        this.form = this.ngFormBuilder.group({
            configSyncPath: ['', [Validators.required, this.validatePath()]],
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
                this.configSyncEnabled = config.options.configSyncEnabled || false;
                this.configSyncPath = config.options.configSyncPath || '';
                this.form.patchValue({
                    configSyncPath: this.configSyncPath,
                });
            }
        } catch (error) {
            // Silently fail if not authenticated yet
        }
    }

    private validatePath(): ValidatorFn {
        return (control: AbstractControl): ValidationErrors | null => {
            if (!control.value || typeof control.value !== 'string') {
                return null;
            }
            const path = control.value.trim();
            if (path.length === 0) {
                return null; // Let required validator handle empty
            }
            // Basic path validation - must be absolute path
            // Windows: C:\ or D:\ or \\server\share or \\?\UNC\server\share
            // Unix/Mac: /path/to/dir
            // Allow Windows drive letters (case insensitive), UNC paths, and Unix absolute paths
            const windowsDrive = /^[A-Za-z]:[\\/]/; // C:\ or C:/
            const windowsUNC = /^\\\\/; // \\server\share
            const unixPath = /^\//; // /path/to/dir
            // Also allow Windows long path format \\?\
            const windowsLongPath = /^\\\\\?\\/;
            
            if (windowsDrive.test(path) || windowsUNC.test(path) || unixPath.test(path) || windowsLongPath.test(path)) {
                return null;
            }
            return { invalid: true };
        };
    }

    async toggleConfigSync(enabled: boolean): Promise<void> {
        const snackConfig: MatSnackBarConfig = { duration: 3000 };
        
        try {
            const currentConfig = await this.adminService.getConfig();
            if (currentConfig && currentConfig.options) {
                currentConfig.options.configSyncEnabled = enabled;
                await this.adminService.saveConfig(currentConfig);
                this.configSyncEnabled = enabled;
                
                const message = enabled 
                    ? 'Config sync enabled' 
                    : 'Config sync disabled';
                this.matSnackBar.open(message, '', snackConfig);
            }
        } catch (error) {
            this.matSnackBar.open('Failed to update config sync setting', '', snackConfig);
            // Revert the toggle on error
            this.configSyncEnabled = !enabled;
        }
    }

    async save(): Promise<void> {
        const snackConfig: MatSnackBarConfig = { duration: 5000 };
        const path = this.form.get('configSyncPath')?.value?.trim();

        if (!path) {
            this.matSnackBar.open('Sync path is required', '', snackConfig);
            return;
        }

        this.form.disable();

        try {
            const currentConfig = await this.adminService.getConfig();
            if (currentConfig && currentConfig.options) {
                currentConfig.options.configSyncPath = path;
                await this.adminService.saveConfig(currentConfig);
                this.configSyncPath = path;
                
                this.matSnackBar.open('Config sync path saved successfully', '', snackConfig);
            }
        } catch (error) {
            this.matSnackBar.open('Failed to save config sync path', '', snackConfig);
        }

        this.form.enable();
    }

    reset(): void {
        this.form.patchValue({
            configSyncPath: this.configSyncPath,
        });
    }
}

