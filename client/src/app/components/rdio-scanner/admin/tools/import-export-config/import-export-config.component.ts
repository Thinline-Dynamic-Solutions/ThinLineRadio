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


import { Component, EventEmitter, Inject, Output, DOCUMENT } from '@angular/core';
import { MatDialog } from '@angular/material/dialog';
import { MatSnackBar } from '@angular/material/snack-bar';
import { Config, RdioScannerAdminService } from '../../admin.service';
import { decodeImportFileBuffer, normalizeImportedConfig } from './config-import.util';
import { ImportConfigResultDialogComponent } from './import-config-result-dialog.component';
import { ImportConfigReviewDialogComponent } from './import-config-review-dialog.component';
import packageInfo from '../../../../../../../package.json';

const ADMIN_IMPORT_DIALOG = {
    panelClass: 'admin-dialog-panel',
    backdropClass: 'admin-dialog-backdrop',
} as const;

@Component({
    selector: 'rdio-scanner-admin-import-export-config',
    styleUrls: ['./import-export-config.component.scss'],
    templateUrl: './import-export-config.component.html',
    standalone: false
})
export class RdioScannerAdminImportExportConfigComponent {
    @Output() config = new EventEmitter<Config & { __isImport?: boolean }>();

    version = packageInfo.version;

    constructor(
        private adminService: RdioScannerAdminService,
        @Inject(DOCUMENT) private document: Document,
        private matSnackBar: MatSnackBar,
        private dialog: MatDialog,
    ) { }

    async importAndApply(event: Event): Promise<void> {
        const target = (event.target as HTMLInputElement & EventTarget);
        const file = target.files?.item(0);
        if (!(file instanceof File)) return;

        const reader = new FileReader();

        reader.onloadend = async () => {
            target.value = '';

            const dialogRef = this.dialog.open(ImportConfigResultDialogComponent, {
                ...ADMIN_IMPORT_DIALOG,
                disableClose: true,
                width: '520px',
                data: {
                    phase: 'loading',
                    statusMessage: 'Reading import file...',
                },
            });

            try {
                dialogRef.componentInstance.setStatus('Preparing configuration...');
                const config = normalizeImportedConfig(
                    JSON.parse(decodeImportFileBuffer(reader.result as string)),
                    { stripLegacyAccess: false },
                );

                dialogRef.componentInstance.setStatus('Uploading and applying to server...');
                const result = await this.adminService.saveConfig(config, true);
                dialogRef.componentInstance.showResults(
                    result.importSummary ?? [],
                    result.importSummaryMessage ?? 'Configuration applied successfully',
                );

                dialogRef.afterClosed().subscribe((success) => {
                    if (success) {
                        this.config.emit(result.config);
                    }
                });

            } catch (error) {
                const message = error instanceof Error ? error.message : String(error);
                dialogRef.componentInstance.showError(message || 'Import failed');
            }
        };

        reader.readAsBinaryString(file);
    }

    async export(): Promise<void> {
        const config = await this.adminService.getConfig();

        const file = encodeURIComponent(JSON.stringify(config)).replace(/%([0-9A-F]{2})/g, (_, c) => {
            return String.fromCharCode(parseInt(c, 16));
        });
        const fileName = `ThinLineRadioV7-config.json`;
        const fileType = 'application/json';
        const fileUri = `data:${fileType};base64,${window.btoa(file)}`;

        const el = this.document.createElement('a');

        el.style.display = 'none';

        el.setAttribute('href', fileUri);
        el.setAttribute('download', fileName);

        this.document.body.appendChild(el);

        el.click();

        this.document.body.removeChild(el);
    }

    async import(event: Event): Promise<void> {
        const target = (event.target as HTMLInputElement & EventTarget);
        const file = target.files?.item(0);
        if (!(file instanceof File)) return;

        const reader = new FileReader();

        reader.onloadend = () => {
            target.value = '';

            try {
                const importedConfig = normalizeImportedConfig(
                    JSON.parse(decodeImportFileBuffer(reader.result as string)),
                );

                const dialogRef = this.dialog.open(ImportConfigReviewDialogComponent, {
                    ...ADMIN_IMPORT_DIALOG,
                    width: '560px',
                    maxHeight: '90vh',
                    data: {
                        importedConfig,
                        fileName: file.name,
                    },
                });

                dialogRef.afterClosed().subscribe((result) => {
                    if (result?.config) {
                        this.config.emit({ ...result.config, __isImport: true });
                    }
                });

            } catch (error) {
                this.matSnackBar.open(error instanceof Error ? error.message : String(error), '', { duration: 5000 });
            }
        };

        reader.readAsBinaryString(file);
    }
}
