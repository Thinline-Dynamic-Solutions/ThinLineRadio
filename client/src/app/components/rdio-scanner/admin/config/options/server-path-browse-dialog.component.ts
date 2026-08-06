/*
 * *****************************************************************************
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

import { Component, Inject, OnInit, ChangeDetectionStrategy, ChangeDetectorRef } from '@angular/core';
import { MatDialogRef, MAT_DIALOG_DATA } from '@angular/material/dialog';
import { RdioScannerAdminService, FsBrowseEntry, FsBrowseResponse } from '../../admin.service';

export interface ServerPathBrowseDialogData {
    /** Initial path on the ThinLine Radio server. */
    initialPath?: string;
    /** Initial file name for config sync. */
    initialFileName?: string;
    title?: string;
}

export interface ServerPathBrowseDialogResult {
    path: string;
    fileName: string;
}

@Component({
    selector: 'rdio-scanner-admin-server-path-browse-dialog',
    template: `
    <h2 mat-dialog-title>{{ data.title || 'Browse server folder' }}</h2>
    <mat-dialog-content class="fs-browse">
      <p class="fs-browse-note">
        This browses folders on the <strong>ThinLine Radio server</strong> disk —
        not the computer running this admin page.
      </p>

      <div class="fs-browse-path-row">
        <mat-form-field appearance="outline" class="fs-browse-path" subscriptSizing="dynamic">
          <mat-label>Server path</mat-label>
          <input matInput [(ngModel)]="pathInput" (keydown.enter)="goToInput()" [disabled]="loading">
        </mat-form-field>
        <button type="button" class="fs-browse-action" (click)="goToInput()" [disabled]="loading">Go</button>
      </div>

      <div class="fs-browse-toolbar">
        <button mat-icon-button type="button" class="fs-browse-up"
          (click)="goUp()"
          [disabled]="loading || !canGoUp"
          matTooltip="Up one level">
          <mat-icon>arrow_upward</mat-icon>
        </button>
        @if (baseDir) {
          <button type="button" class="fs-browse-action" (click)="goTo(baseDir)" [disabled]="loading">
            Base dir
          </button>
        }
        <span class="fs-browse-current" [title]="currentPath">{{ currentPath || '(root)' }}</span>
      </div>

      @if (loading) {
        <div class="fs-browse-status">
          <mat-spinner diameter="32"></mat-spinner>
          <span>Loading…</span>
        </div>
      }

      @if (!loading && error) {
        <div class="fs-browse-error">{{ error }}</div>
      }

      @if (!loading && !error) {
        <div class="fs-browse-list">
          @if (!entries.length) {
            <div class="fs-browse-empty">No subfolders here</div>
          }
          @for (entry of entries; track entry.path) {
            <button type="button" class="fs-browse-item" (click)="goTo(entry.path)">
              <mat-icon>folder</mat-icon>
              <span>{{ entry.name }}</span>
              <mat-icon class="fs-browse-chevron">chevron_right</mat-icon>
            </button>
          }
        </div>
      }

      <div class="fs-browse-filename-row">
        <mat-form-field appearance="outline" class="fs-browse-path" subscriptSizing="dynamic">
          <mat-label>File name</mat-label>
          <input matInput [(ngModel)]="fileName" placeholder="ThinLineRadioV7-config.json" autocomplete="off">
        </mat-form-field>
      </div>
    </mat-dialog-content>
    <mat-dialog-actions align="end">
      <button mat-button type="button" (click)="cancel()">Cancel</button>
      <button mat-raised-button color="primary" type="button"
        [disabled]="loading || !currentPath || !fileName.trim()"
        (click)="selectCurrent()">
        Use this location
      </button>
    </mat-dialog-actions>
  `,
    styles: [`
    :host {
      display: block;
      color: #e0e0e0;
    }
    .fs-browse {
      box-sizing: border-box;
      width: min(760px, 92vw);
      min-height: 420px;
    }
    .fs-browse-note {
      margin: 0 0 16px;
      font-size: 13px;
      color: rgba(255, 255, 255, 0.55);
      line-height: 1.45;
    }
    .fs-browse-path-row {
      display: flex;
      gap: 10px;
      align-items: flex-start;
      margin-bottom: 10px;
    }
    .fs-browse-path {
      flex: 1;
      margin: 0 !important;
    }
    .fs-browse-action {
      appearance: none;
      -webkit-appearance: none;
      flex-shrink: 0;
      height: 40px;
      padding: 0 14px;
      border-radius: 4px;
      border: 1px solid rgba(255, 255, 255, 0.45);
      background: rgba(255, 255, 255, 0.12);
      color: #f5f5f5;
      font: inherit;
      font-size: 13px;
      font-weight: 600;
      cursor: pointer;
    }
    .fs-browse-action:hover:not(:disabled) {
      background: rgba(255, 255, 255, 0.2);
      border-color: rgba(255, 255, 255, 0.65);
    }
    .fs-browse-action:disabled {
      opacity: 0.4;
      cursor: default;
    }
    .fs-browse-toolbar {
      display: flex;
      align-items: center;
      gap: 8px;
      margin-bottom: 12px;
    }
    .fs-browse-up {
      color: #f5f5f5 !important;
      background: rgba(255, 255, 255, 0.1) !important;
      border: 1px solid rgba(255, 255, 255, 0.35);
      border-radius: 4px;
    }
    .fs-browse-up:disabled {
      opacity: 0.35;
    }
    .fs-browse-current {
      flex: 1;
      min-width: 0;
      font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
      font-size: 12px;
      color: rgba(255, 255, 255, 0.72);
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }
    .fs-browse-status {
      display: flex;
      align-items: center;
      justify-content: center;
      gap: 14px;
      min-height: 360px;
      color: #888;
    }
    .fs-browse-error {
      padding: 14px 16px;
      border-radius: 6px;
      border: 1px solid rgba(244, 67, 54, 0.35);
      background: rgba(244, 67, 54, 0.12);
      color: #ef9a9a;
      font-size: 13px;
    }
    .fs-browse-list {
      max-height: min(52vh, 480px);
      min-height: 320px;
      overflow: auto;
      border: 1px solid #333;
      border-radius: 6px;
      background: #161616;
    }
    .fs-browse-empty {
      padding: 48px 20px;
      text-align: center;
      color: #666;
      font-size: 13px;
    }
    .fs-browse-item {
      display: flex;
      align-items: center;
      gap: 10px;
      width: 100%;
      padding: 10px 14px;
      border: none;
      border-bottom: 1px solid #2a2a2a;
      background: transparent;
      color: #e0e0e0;
      font: inherit;
      font-size: 13.5px;
      text-align: left;
      cursor: pointer;
    }
    .fs-browse-item:hover {
      background: rgba(204, 0, 0, 0.08);
    }
    .fs-browse-item > span {
      flex: 1;
      min-width: 0;
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }
    .fs-browse-item > mat-icon:first-child {
      color: #e53935;
      font-size: 22px;
      width: 22px;
      height: 22px;
      flex-shrink: 0;
    }
    .fs-browse-chevron {
      color: #888;
      font-size: 18px;
      width: 18px;
      height: 18px;
      flex-shrink: 0;
    }
    .fs-browse-filename-row {
      margin-top: 14px;
    }
  `],
    changeDetection: ChangeDetectionStrategy.OnPush,
    standalone: false
})
export class ServerPathBrowseDialogComponent implements OnInit {
    loading = false;
    error = '';
    currentPath = '';
    parent = '';
    baseDir = '';
    pathInput = '';
    fileName = 'ThinLineRadioV7-config.json';
    entries: FsBrowseEntry[] = [];

    constructor(
        private dialogRef: MatDialogRef<ServerPathBrowseDialogComponent, ServerPathBrowseDialogResult | null>,
        private adminService: RdioScannerAdminService,
        private cdr: ChangeDetectorRef,
        @Inject(MAT_DIALOG_DATA) public data: ServerPathBrowseDialogData,
    ) {}

    get canGoUp(): boolean {
        if (!this.currentPath || this.currentPath === '/') {
            return false;
        }
        return this.parent !== '';
    }

    ngOnInit(): void {
        const start = (this.data.initialPath || '').trim();
        const name = (this.data.initialFileName || '').trim();
        if (name) {
            this.fileName = name;
        }
        void this.load(start);
    }

    async load(path: string): Promise<void> {
        this.loading = true;
        this.error = '';
        this.cdr.markForCheck();
        try {
            const res: FsBrowseResponse = await this.adminService.browseServerFs(path);
            this.currentPath = res.path || '';
            this.parent = res.parent || '';
            this.baseDir = res.baseDir || '';
            this.pathInput = this.currentPath;
            this.entries = res.entries || [];
            this.error = res.error || '';
            // First open with empty path lands on "/"; jump to baseDir when available.
            if (!path && this.baseDir && this.currentPath === '/' && !this.error) {
                await this.load(this.baseDir);
                return;
            }
        } catch (e: any) {
            this.error = e?.message || 'Failed to browse server filesystem';
            this.entries = [];
        } finally {
            this.loading = false;
            this.cdr.markForCheck();
        }
    }

    goTo(path: string): void {
        void this.load(path);
    }

    goUp(): void {
        if (!this.canGoUp) {
            return;
        }
        void this.load(this.parent);
    }

    goToInput(): void {
        void this.load(this.pathInput.trim());
    }

    selectCurrent(): void {
        const name = this.fileName.trim();
        if (this.currentPath && name) {
            this.dialogRef.close({ path: this.currentPath, fileName: name });
        }
    }

    cancel(): void {
        this.dialogRef.close(null);
    }
}
