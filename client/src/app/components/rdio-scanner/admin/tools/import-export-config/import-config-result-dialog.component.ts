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

import { ChangeDetectorRef, Component, Inject, ChangeDetectionStrategy } from '@angular/core';
import { MAT_DIALOG_DATA, MatDialogRef } from '@angular/material/dialog';
import { ConfigImportSummaryItem } from '../../admin.service';

export type ImportConfigDialogPhase = 'loading' | 'results' | 'error';

export interface ImportConfigResultDialogData {
    phase: ImportConfigDialogPhase;
    statusMessage?: string;
    summary?: ConfigImportSummaryItem[];
    summaryMessage?: string;
    errorMessage?: string;
}

@Component({
    selector: 'rdio-scanner-admin-import-config-result-dialog',
    templateUrl: './import-config-result-dialog.component.html',
    styleUrls: ['./import-config-result-dialog.component.scss'],
    changeDetection: ChangeDetectionStrategy.Eager,
    standalone: false
})
export class ImportConfigResultDialogComponent {
    phase: ImportConfigDialogPhase = 'loading';
    statusMessage = 'Reading import file...';
    summary: ConfigImportSummaryItem[] = [];
    summaryMessage = '';
    errorMessage = '';

    constructor(
        private dialogRef: MatDialogRef<ImportConfigResultDialogComponent>,
        @Inject(MAT_DIALOG_DATA) data: ImportConfigResultDialogData,
        private cdr: ChangeDetectorRef,
    ) {
        this.applyData(data);
    }

    setStatus(message: string): void {
        this.phase = 'loading';
        this.statusMessage = message;
        this.cdr.markForCheck();
    }

    showResults(summary: ConfigImportSummaryItem[], summaryMessage: string): void {
        this.phase = 'results';
        this.summary = summary;
        this.summaryMessage = summaryMessage;
        this.cdr.markForCheck();
    }

    showError(message: string): void {
        this.phase = 'error';
        this.errorMessage = message;
        this.cdr.markForCheck();
    }

    get allOk(): boolean {
        return this.summary.length > 0 && this.summary.every(item => item.ok);
    }

    close(): void {
        this.dialogRef.close(this.phase === 'results' && this.allOk);
    }

    private applyData(data: ImportConfigResultDialogData): void {
        this.phase = data.phase;
        this.statusMessage = data.statusMessage ?? this.statusMessage;
        this.summary = data.summary ?? [];
        this.summaryMessage = data.summaryMessage ?? '';
        this.errorMessage = data.errorMessage ?? '';
    }
}
