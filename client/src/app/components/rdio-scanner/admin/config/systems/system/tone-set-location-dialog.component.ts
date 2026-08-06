/*
 * Copyright (C) 2025 Thinline Dynamic Solutions
 *
 * Assign incident-mapping locations to tone sets. Admins can fill rows
 * manually or use Suggest locations (Gemini place names + TLR geocode).
 * Nothing is persisted until Apply.
 */

import { Component, Inject } from '@angular/core';
import { MatDialogRef, MAT_DIALOG_DATA } from '@angular/material/dialog';
import { MatSnackBar } from '@angular/material/snack-bar';
import { RdioScannerAdminService } from '../../../admin.service';
import {
    MappingToneSetLocationApply,
    MappingToneSetLocationRow,
} from '../../../../mapping/mapping.types';

interface ToneSetLocationDialogData {
    systemId: number;
}

interface DialogRow extends Omit<MappingToneSetLocationRow, 'geoLat' | 'geoLon'> {
    geoLat?: number | null;
    geoLon?: number | null;
}

@Component({
    selector: 'rdio-scanner-tone-set-location-dialog',
    template: `
    <h2 mat-dialog-title>Assign locations to tone sets</h2>
    <mat-dialog-content>
        <p class="intro">
            When a tone set matches on a dispatch call, its location takes over on the incident map
            and the parent talkgroup geo is not used. Use <strong>Suggest locations</strong> to draft
            city/label and coordinates (Gemini + TLR geocoding), review the table, then click
            <strong>Apply</strong> to save. Existing coordinates are left alone.
        </p>

        <div class="tsl-table" *ngIf="rows.length; else noRows">
            <div class="tsl-head">
                <span class="c-tg">Talkgroup</span>
                <span class="c-label">Tone set</span>
                <span class="c-query">City / label</span>
                <span class="c-num">Lat</span>
                <span class="c-num">Lon</span>
                <span class="c-num">Radius</span>
                <span class="c-actions"></span>
            </div>
            <div class="tsl-row" *ngFor="let row of rows">
                <span class="c-tg" [title]="row.talkgroupLabel">{{ row.talkgroupLabel }}</span>
                <span class="c-label" [title]="row.label">{{ row.label }}</span>
                <span class="c-query">
                    <input type="text" [(ngModel)]="row.geoCity" placeholder="City, township, zip…">
                </span>
                <span class="c-num">
                    <input type="number" step="any" [(ngModel)]="row.geoLat">
                </span>
                <span class="c-num">
                    <input type="number" step="any" [(ngModel)]="row.geoLon">
                </span>
                <span class="c-num">
                    <input type="number" step="any" [(ngModel)]="row.geoRadiusMiles" min="1" max="40">
                </span>
                <span class="c-actions">
                    <button mat-icon-button type="button" color="warn" (click)="clearRow(row)" aria-label="Clear">
                        <mat-icon>clear</mat-icon>
                    </button>
                </span>
            </div>
        </div>
        <ng-template #noRows>
            <p class="intro">No tone sets found on this system. Configure tone sets on talkgroups first.</p>
        </ng-template>
    </mat-dialog-content>
    <mat-dialog-actions align="end">
        <button mat-button type="button" (click)="onCancel()">Cancel</button>
        <button mat-stroked-button color="accent" type="button"
            [disabled]="suggesting || applying || loading || !rows.length"
            (click)="onSuggest()">
            {{ suggesting ? 'Suggesting…' : 'Suggest locations' }}
        </button>
        <button mat-raised-button color="primary" type="button" [disabled]="applying || suggesting || !rows.length" (click)="onApply()">
            {{ applying ? 'Saving…' : 'Apply' }}
        </button>
    </mat-dialog-actions>
    `,
    styles: [`
        mat-dialog-content { min-width: 820px; max-width: 980px; max-height: calc(90vh - 130px); }
        .intro { font-size: 13px; opacity: 0.85; line-height: 1.45; margin: 0 0 14px; }
        .tsl-table { display: flex; flex-direction: column; gap: 2px; }
        .tsl-head, .tsl-row {
            display: grid;
            grid-template-columns: 1.2fr 1fr 1.6fr 0.85fr 0.85fr 0.65fr 48px;
            gap: 8px;
            align-items: start;
        }
        .tsl-head { font-size: 11px; text-transform: uppercase; opacity: 0.6; padding: 0 0 6px; border-bottom: 1px solid rgba(255,255,255,0.12); }
        .tsl-row { padding: 6px 0; border-bottom: 1px solid rgba(255,255,255,0.05); }
        .c-tg, .c-label { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; font-size: 13px; padding-top: 8px; }
        .c-query input, .c-num input {
            width: 100%; box-sizing: border-box; padding: 6px 8px;
            background: rgba(255,255,255,0.06); border: 1px solid rgba(255,255,255,0.15);
            border-radius: 4px; color: inherit; font-size: 13px;
        }
        .c-actions { display: flex; gap: 4px; align-items: center; padding-top: 2px; }
    `],
    standalone: false
})
export class ToneSetLocationDialogComponent {
    rows: DialogRow[] = [];
    applying = false;
    suggesting = false;
    loading = true;

    constructor(
        private adminService: RdioScannerAdminService,
        private snackBar: MatSnackBar,
        public dialogRef: MatDialogRef<ToneSetLocationDialogComponent>,
        @Inject(MAT_DIALOG_DATA) public data: ToneSetLocationDialogData,
    ) {
        void this.loadRows();
    }

    private async loadRows(): Promise<void> {
        try {
            const res = await this.adminService.listToneSetLocations(this.data.systemId);
            this.rows = (res.toneSets || []).map((r) => ({
                ...r,
                geoRadiusMiles: r.geoRadiusMiles || 5,
            }));
        } catch (err: any) {
            this.snackBar.open(err?.error?.error || err?.message || 'Failed to load tone sets', 'Close', { duration: 6000 });
        } finally {
            this.loading = false;
        }
    }

    clearRow(row: DialogRow): void {
        row.geoCity = '';
        row.geoLat = null;
        row.geoLon = null;
        row.geoRadiusMiles = 5;
        row.locationContext = '';
    }

    onCancel(): void {
        this.dialogRef.close(false);
    }

    async onSuggest(): Promise<void> {
        this.suggesting = true;
        try {
            const res = await this.adminService.suggestToneSetLocations(this.data.systemId, true);
            const byKey = new Map(
                (res.toneSets || []).map((s) => [`${s.talkgroupId}|${s.toneSetId}`, s]),
            );
            for (const row of this.rows) {
                if (row.geoLat || row.geoLon) {
                    continue;
                }
                const sug = byKey.get(`${row.talkgroupId}|${row.toneSetId}`);
                if (!sug || sug.source === 'skipped') {
                    continue;
                }
                if (sug.geoCity) {
                    row.geoCity = sug.geoCity;
                }
                if (sug.locationContext) {
                    row.locationContext = sug.locationContext;
                }
                if (sug.geoLat) {
                    row.geoLat = sug.geoLat;
                }
                if (sug.geoLon) {
                    row.geoLon = sug.geoLon;
                }
                if (sug.geoRadiusMiles) {
                    row.geoRadiusMiles = sug.geoRadiusMiles;
                }
            }
            this.snackBar.open(
                `Filled ${res.filled}, skipped ${res.skipped}, failed ${res.failed}`,
                'Close',
                { duration: 7000 },
            );
        } catch (err: any) {
            this.snackBar.open(
                err?.error?.error || err?.message || 'Suggest failed',
                'Close',
                { duration: 8000 },
            );
        } finally {
            this.suggesting = false;
        }
    }

    async onApply(): Promise<void> {
        this.applying = true;
        try {
            const toneSets: MappingToneSetLocationApply[] = this.rows.map((r) => {
                const clear = !r.geoLat && !r.geoLon && !(r.geoCity || '').trim();
                return {
                    talkgroupId: r.talkgroupId,
                    toneSetId: r.toneSetId,
                    geoCity: (r.geoCity || '').trim(),
                    geoLat: r.geoLat ?? 0,
                    geoLon: r.geoLon ?? 0,
                    geoRadiusMiles: r.geoRadiusMiles || 5,
                    locationContext: r.locationContext || '',
                    clear,
                };
            });
            const res = await this.adminService.applyToneSetLocations(this.data.systemId, toneSets);
            this.snackBar.open(
                `Saved ${res.applied} tone set location${res.applied === 1 ? '' : 's'}` +
                (res.cleared ? `, cleared ${res.cleared}` : ''),
                'Close',
                { duration: 6000 },
            );
            this.dialogRef.close(true);
        } catch (err: any) {
            this.snackBar.open(err?.error?.error || err?.message || 'Save failed', 'Close', { duration: 6000 });
        } finally {
            this.applying = false;
        }
    }
}
