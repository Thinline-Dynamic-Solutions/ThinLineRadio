/*
 * *****************************************************************************
 * Copyright (C) 2025 Thinline Dynamic Solutions
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 * ****************************************************************************
 */

import { Component, Inject } from '@angular/core';
import { MAT_DIALOG_DATA, MatDialogRef } from '@angular/material/dialog';
import { Config, RdioScannerAdminService } from '../../admin.service';
import { ConfigImportSection, listConfigImportSections, mergeConfigSections } from './config-import.util';

export interface ImportConfigReviewDialogData {
    importedConfig: Config;
    fileName: string;
}

export interface ImportConfigReviewDialogResult {
    config: Config;
}

@Component({
    selector: 'rdio-scanner-admin-import-config-review-dialog',
    templateUrl: './import-config-review-dialog.component.html',
    styleUrls: ['./import-config-review-dialog.component.scss'],
    standalone: false
})
export class ImportConfigReviewDialogComponent {
    sections: ConfigImportSection[] = [];
    selectedKeys = new Set<string>();
    loading = false;
    errorMessage = '';

    constructor(
        private dialogRef: MatDialogRef<ImportConfigReviewDialogComponent, ImportConfigReviewDialogResult | undefined>,
        @Inject(MAT_DIALOG_DATA) public data: ImportConfigReviewDialogData,
        private adminService: RdioScannerAdminService,
    ) {
        this.sections = listConfigImportSections(data.importedConfig);
        this.sections.forEach(section => this.selectedKeys.add(section.key));
    }

    get allSelected(): boolean {
        return this.sections.length > 0 && this.selectedKeys.size === this.sections.length;
    }

    get someSelected(): boolean {
        return this.selectedKeys.size > 0 && !this.allSelected;
    }

    get selectedCount(): number {
        return this.selectedKeys.size;
    }

    get hasSystemsWithoutGroups(): boolean {
        return this.selectedKeys.has('systems')
            && !this.selectedKeys.has('groups')
            && this.sections.some(s => s.key === 'groups');
    }

    get hasSystemsWithoutTags(): boolean {
        return this.selectedKeys.has('systems')
            && !this.selectedKeys.has('tags')
            && this.sections.some(s => s.key === 'tags');
    }

    get hasUsersWithoutUserGroups(): boolean {
        return this.selectedKeys.has('users')
            && !this.selectedKeys.has('userGroups')
            && this.sections.some(s => s.key === 'userGroups');
    }

    isSelected(key: string): boolean {
        return this.selectedKeys.has(key);
    }

    toggleSection(key: string, checked: boolean): void {
        if (checked) {
            this.selectedKeys.add(key);
        } else {
            this.selectedKeys.delete(key);
        }
    }

    toggleAll(checked: boolean): void {
        if (checked) {
            this.sections.forEach(section => this.selectedKeys.add(section.key));
        } else {
            this.selectedKeys.clear();
        }
    }

    cancel(): void {
        this.dialogRef.close(undefined);
    }

    async continueToReview(): Promise<void> {
        if (this.selectedKeys.size === 0) {
            return;
        }

        this.loading = true;
        this.errorMessage = '';

        try {
            const current = await this.adminService.getConfig();
            const merged = mergeConfigSections(current, this.data.importedConfig, [...this.selectedKeys]);
            this.dialogRef.close({ config: merged });
        } catch (error) {
            this.errorMessage = error instanceof Error ? error.message : String(error);
            this.loading = false;
        }
    }
}
