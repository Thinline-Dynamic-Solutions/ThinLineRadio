/*
 * Copyright (C) 2025 Thinline Dynamic Solutions
 *
 * Bulk-assign incident-mapping locations to talkgroups. Suggest drafts
 * city/label + coords (Gemini + TLR geocode) in batches of 50; Apply persists.
 * Clear returns a talkgroup to inherit system geo.
 */

import { Component, Inject } from '@angular/core';
import { MatDialogRef, MAT_DIALOG_DATA } from '@angular/material/dialog';
import { MatSnackBar } from '@angular/material/snack-bar';
import { RdioScannerAdminService } from '../../../admin.service';
import {
    MappingTalkgroupLocationApply,
    MappingTalkgroupLocationRow,
    MappingTalkgroupLocationSuggest,
} from '../../../../mapping/mapping.types';

interface TalkgroupLocationDialogData {
    systemId: number;
}

interface DialogRow extends Omit<MappingTalkgroupLocationRow, 'geoLat' | 'geoLon'> {
    geoLat?: number | null;
    geoLon?: number | null;
}

const SUGGEST_BATCH_SIZE = 50;

@Component({
    selector: 'rdio-scanner-talkgroup-location-dialog',
    template: `
    <h2 mat-dialog-title>Assign locations to talkgroups</h2>
    <mat-dialog-content>
        <p class="intro">
            Set each talkgroup’s map jurisdiction (city/label + coordinates).
            Use <strong>Suggest locations</strong> to draft values (Gemini + TLR geocoding),
            review the table, then <strong>Apply</strong> to save.
            Applying turns off inherit for that talkgroup. Clearing a row returns it to inherit system geo.
            Talkgroups that already have their own coordinates are left alone by Suggest.
            Large systems are processed in batches of {{ batchSize }}.
        </p>

        <div class="suggest-progress" *ngIf="suggesting">
            <mat-progress-bar mode="determinate" [value]="suggestProgressPct"></mat-progress-bar>
            <span class="suggest-progress-label">{{ suggestProgressLabel }}</span>
        </div>

        <div class="tsl-table" *ngIf="rows.length; else noRows">
            <div class="tsl-head">
                <span class="c-tg">Talkgroup</span>
                <span class="c-mode">Mode</span>
                <span class="c-query">City / label</span>
                <span class="c-num">Lat</span>
                <span class="c-num">Lon</span>
                <span class="c-num">Radius</span>
                <span class="c-actions"></span>
            </div>
            <div class="tsl-row" *ngFor="let row of rows">
                <span class="c-tg" [title]="row.talkgroupLabel">{{ row.talkgroupLabel }}</span>
                <span class="c-mode">{{ row.inherit ? 'Inherit' : 'Own' }}</span>
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
                    <button mat-icon-button type="button" color="warn" (click)="clearRow(row)" aria-label="Clear to inherit">
                        <mat-icon>clear</mat-icon>
                    </button>
                </span>
            </div>
        </div>
        <ng-template #noRows>
            <p class="intro">No talkgroups found on this system.</p>
        </ng-template>
    </mat-dialog-content>
    <mat-dialog-actions align="end">
        <button mat-button type="button" (click)="onCancel()" [disabled]="suggesting">Cancel</button>
        <button mat-stroked-button color="accent" type="button"
            [disabled]="suggesting || applying || loading || !rows.length"
            (click)="onSuggest()">
            {{ suggesting ? suggestButtonLabel : 'Suggest locations' }}
        </button>
        <button mat-raised-button color="primary" type="button" [disabled]="applying || suggesting || !rows.length" (click)="onApply()">
            {{ applying ? 'Saving…' : 'Apply' }}
        </button>
    </mat-dialog-actions>
    `,
    styles: [`
        mat-dialog-content { min-width: 820px; max-width: 980px; max-height: calc(90vh - 130px); }
        .intro { font-size: 13px; opacity: 0.85; line-height: 1.45; margin: 0 0 14px; }
        .suggest-progress {
            margin: 0 0 14px;
            display: flex;
            flex-direction: column;
            gap: 6px;
        }
        .suggest-progress-label { font-size: 12px; opacity: 0.8; }
        .tsl-table { display: flex; flex-direction: column; gap: 2px; }
        .tsl-head, .tsl-row {
            display: grid;
            grid-template-columns: 1.4fr 0.7fr 1.6fr 0.85fr 0.85fr 0.65fr 48px;
            gap: 8px;
            align-items: start;
        }
        .tsl-head { font-size: 11px; text-transform: uppercase; opacity: 0.6; padding: 0 0 6px; border-bottom: 1px solid rgba(255,255,255,0.12); }
        .tsl-row { padding: 6px 0; border-bottom: 1px solid rgba(255,255,255,0.05); }
        .c-tg, .c-mode { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; font-size: 13px; padding-top: 8px; }
        .c-mode { opacity: 0.7; font-size: 12px; }
        .c-query input, .c-num input {
            width: 100%; box-sizing: border-box; padding: 6px 8px;
            background: rgba(255,255,255,0.06); border: 1px solid rgba(255,255,255,0.15);
            border-radius: 4px; color: inherit; font-size: 13px;
        }
        .c-actions { display: flex; gap: 4px; align-items: center; padding-top: 2px; }
    `],
    standalone: false
})
export class TalkgroupLocationDialogComponent {
    readonly batchSize = SUGGEST_BATCH_SIZE;

    rows: DialogRow[] = [];
    applying = false;
    suggesting = false;
    loading = true;
    suggestProgressPct = 0;
    suggestProgressLabel = '';
    suggestButtonLabel = 'Suggesting…';

    constructor(
        private adminService: RdioScannerAdminService,
        private snackBar: MatSnackBar,
        public dialogRef: MatDialogRef<TalkgroupLocationDialogComponent>,
        @Inject(MAT_DIALOG_DATA) public data: TalkgroupLocationDialogData,
    ) {
        void this.loadRows();
    }

    private async loadRows(): Promise<void> {
        try {
            const res = await this.adminService.listTalkgroupLocations(this.data.systemId);
            this.rows = (res.talkgroups || []).map((r) => ({
                ...r,
                geoRadiusMiles: r.geoRadiusMiles || 25,
            }));
        } catch (err: any) {
            this.snackBar.open(err?.error?.error || err?.message || 'Failed to load talkgroups', 'Close', { duration: 6000 });
        } finally {
            this.loading = false;
        }
    }

    clearRow(row: DialogRow): void {
        row.geoCity = '';
        row.geoLat = null;
        row.geoLon = null;
        row.geoRadiusMiles = 25;
        row.locationContext = '';
        row.inherit = true;
    }

    onCancel(): void {
        this.dialogRef.close(false);
    }

    private idsNeedingSuggest(): number[] {
        return this.rows
            .filter((r) => r.inherit || !(r.geoLat || r.geoLon))
            .map((r) => r.talkgroupId);
    }

    private applySuggestions(suggestions: MappingTalkgroupLocationSuggest[]): void {
        const byId = new Map(suggestions.map((s) => [s.talkgroupId, s]));
        for (const row of this.rows) {
            if (!row.inherit && (row.geoLat || row.geoLon)) {
                continue;
            }
            const sug = byId.get(row.talkgroupId);
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
            if (row.geoLat || row.geoLon || row.geoCity) {
                row.inherit = false;
            }
        }
    }

    async onSuggest(): Promise<void> {
        const ids = this.idsNeedingSuggest();
        if (!ids.length) {
            this.snackBar.open('Nothing to suggest — all talkgroups already have their own coordinates.', 'Close', { duration: 5000 });
            return;
        }

        const batches: number[][] = [];
        for (let i = 0; i < ids.length; i += SUGGEST_BATCH_SIZE) {
            batches.push(ids.slice(i, i + SUGGEST_BATCH_SIZE));
        }

        this.suggesting = true;
        this.suggestProgressPct = 0;
        this.suggestProgressLabel = `Starting… ${ids.length} talkgroup${ids.length === 1 ? '' : 's'} in ${batches.length} batch${batches.length === 1 ? '' : 'es'}`;
        this.suggestButtonLabel = `Batch 0/${batches.length}`;

        let filled = 0;
        let skipped = 0;
        let failed = 0;
        let batchErrors = 0;

        try {
            for (let i = 0; i < batches.length; i++) {
                const batch = batches[i];
                const from = i * SUGGEST_BATCH_SIZE + 1;
                const to = i * SUGGEST_BATCH_SIZE + batch.length;
                this.suggestButtonLabel = `Batch ${i + 1}/${batches.length}`;
                this.suggestProgressLabel =
                    `Suggesting batch ${i + 1} of ${batches.length} ` +
                    `(talkgroups ${from}–${to} of ${ids.length})…`;
                this.suggestProgressPct = Math.round((i / batches.length) * 100);

                try {
                    const res = await this.adminService.suggestTalkgroupLocations(
                        this.data.systemId,
                        true,
                        batch,
                    );
                    this.applySuggestions(res.talkgroups || []);
                    filled += res.filled || 0;
                    skipped += res.skipped || 0;
                    failed += res.failed || 0;
                } catch (err: any) {
                    batchErrors++;
                    failed += batch.length;
                    console.error(`Talkgroup suggest batch ${i + 1} failed`, err);
                    this.snackBar.open(
                        err?.error?.error || err?.message || `Batch ${i + 1} failed — continuing`,
                        'Close',
                        { duration: 5000 },
                    );
                }

                this.suggestProgressPct = Math.round(((i + 1) / batches.length) * 100);
            }

            this.suggestProgressLabel = `Done — ${batches.length} batch${batches.length === 1 ? '' : 'es'}`;
            let msg = `Filled ${filled}, skipped ${skipped}, failed ${failed}`;
            if (batchErrors) {
                msg += ` (${batchErrors} batch error${batchErrors === 1 ? '' : 's'})`;
            }
            this.snackBar.open(msg, 'Close', { duration: 8000 });
        } finally {
            this.suggesting = false;
            this.suggestProgressPct = 0;
            this.suggestProgressLabel = '';
            this.suggestButtonLabel = 'Suggesting…';
        }
    }

    async onApply(): Promise<void> {
        this.applying = true;
        try {
            const talkgroups: MappingTalkgroupLocationApply[] = this.rows.map((r) => {
                const clear = !r.geoLat && !r.geoLon && !(r.geoCity || '').trim();
                return {
                    talkgroupId: r.talkgroupId,
                    geoCity: (r.geoCity || '').trim(),
                    geoLat: r.geoLat ?? 0,
                    geoLon: r.geoLon ?? 0,
                    geoRadiusMiles: r.geoRadiusMiles || 25,
                    locationContext: r.locationContext || '',
                    clear,
                };
            });
            const res = await this.adminService.applyTalkgroupLocations(this.data.systemId, talkgroups);
            this.snackBar.open(
                `Saved ${res.applied} talkgroup location${res.applied === 1 ? '' : 's'}` +
                (res.cleared ? `, cleared ${res.cleared} to inherit` : ''),
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
