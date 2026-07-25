/*
 * Copyright (C) 2025 Thinline Dynamic Solutions
 */

import {
    ChangeDetectionStrategy,
    ChangeDetectorRef,
    Component,
    Input,
    OnChanges,
    SimpleChanges,
} from '@angular/core';
import { MatSnackBar } from '@angular/material/snack-bar';
import {
    MappingCorrectionRow,
    MappingDataResponse,
} from '../../../mapping/mapping.types';
import { RdioScannerAdminService } from '../../admin.service';

@Component({
    selector: 'rdio-scanner-admin-mapping-data',
    templateUrl: './mapping-data.component.html',
    styleUrls: ['./mapping-data.component.scss'],
    changeDetection: ChangeDetectionStrategy.OnPush,
})
export class RdioScannerAdminMappingDataComponent implements OnChanges {
    @Input() systemId: number | null = null;
    @Input() talkgroupId: number | null = null;

    loading = false;
    corrections: MappingCorrectionRow[] = [];
    stats: MappingDataResponse['stats'] | null = null;

    newBadName = '';
    newCorrectName = '';

    constructor(
        private adminService: RdioScannerAdminService,
        private snackBar: MatSnackBar,
        private cdr: ChangeDetectorRef,
    ) {}

    ngOnChanges(changes: SimpleChanges): void {
        if (changes['systemId'] || changes['talkgroupId']) {
            this.reload();
        }
    }

    get canManage(): boolean {
        return !!this.systemId && this.systemId > 0;
    }

    async reload(): Promise<void> {
        if (!this.canManage) {
            this.corrections = [];
            this.stats = null;
            this.cdr.markForCheck();
            return;
        }
        this.loading = true;
        this.cdr.markForCheck();
        try {
            const data = await this.adminService.getMappingData(
                this.systemId!,
                this.talkgroupId || undefined,
            );
            this.corrections = data?.corrections || [];
            this.stats = data?.stats || null;
        } catch (err: any) {
            this.snackBar.open(err?.error?.error || 'Failed to load mapping data', 'Close', { duration: 4000 });
        } finally {
            this.loading = false;
            this.cdr.markForCheck();
        }
    }

    async addCorrection(): Promise<void> {
        if (!this.newBadName.trim() || !this.newCorrectName.trim() || !this.canManage) {
            return;
        }
        try {
            await this.adminService.addMappingRow(
                this.systemId!,
                'correction',
                {
                    badName: this.newBadName.trim(),
                    correctName: this.newCorrectName.trim(),
                },
                this.talkgroupId || undefined,
            );
            this.newBadName = '';
            this.newCorrectName = '';
            await this.reload();
            this.snackBar.open('Added', 'Close', { duration: 1500 });
        } catch (err: any) {
            this.snackBar.open(err?.error?.error || 'Save failed', 'Close', { duration: 4000 });
        }
    }

    async deleteRow(id: number): Promise<void> {
        if (!this.canManage) {
            return;
        }
        if (!confirm('Are you sure you want to delete this mapping correction?')) {
            return;
        }
        try {
            await this.adminService.deleteMappingRow('correction', id);
            await this.reload();
            this.snackBar.open('Deleted', 'Close', { duration: 1500 });
        } catch (err: any) {
            this.snackBar.open(err?.error?.error || 'Delete failed', 'Close', { duration: 4000 });
        }
    }
}
