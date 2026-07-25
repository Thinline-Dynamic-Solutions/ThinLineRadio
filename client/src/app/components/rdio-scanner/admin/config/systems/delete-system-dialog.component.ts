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

import { Component, Inject } from '@angular/core';
import { MatDialogRef, MAT_DIALOG_DATA } from '@angular/material/dialog';
import { FormBuilder, FormGroup } from '@angular/forms';

export interface DeleteSystemDialogData {
  /** The system's display name/label the operator must type back exactly. */
  systemLabel: string;
}

@Component({
  selector: 'rdio-scanner-delete-system-dialog',
  template: `
    <h2 mat-dialog-title>Delete System</h2>
    <mat-dialog-content>
      <p class="mat-body" style="margin-bottom: 12px;">
        This permanently deletes <strong>{{ data.systemLabel }}</strong> — its talkgroups, sites,
        units, tone sets, and all associated calls. This cannot be undone.
      </p>
      <p class="mat-body" style="margin-bottom: 8px;">
        Type <strong>{{ data.systemLabel }}</strong> below to confirm:
      </p>
      <form [formGroup]="confirmForm">
        <mat-form-field appearance="outline" class="full-width">
          <mat-label>System name</mat-label>
          <input matInput formControlName="confirmText" autocomplete="off"
                 (keydown.enter)="matches() && onConfirm()">
        </mat-form-field>
      </form>
    </mat-dialog-content>
    <mat-dialog-actions align="end">
      <button mat-button (click)="onCancel()">Cancel</button>
      <button mat-raised-button color="warn"
              [disabled]="!matches()"
              (click)="onConfirm()">
        Delete System
      </button>
    </mat-dialog-actions>
  `,
})
export class DeleteSystemDialogComponent {
  confirmForm: FormGroup;

  constructor(
    private dialogRef: MatDialogRef<DeleteSystemDialogComponent, boolean>,
    private formBuilder: FormBuilder,
    @Inject(MAT_DIALOG_DATA) public data: DeleteSystemDialogData,
  ) {
    this.confirmForm = this.formBuilder.group({ confirmText: [''] });
  }

  /** Exact, case-sensitive match — same behavior as GitHub's "type to confirm". */
  matches(): boolean {
    const typed = this.confirmForm.get('confirmText')?.value ?? '';
    return typed === this.data.systemLabel;
  }

  onConfirm(): void {
    if (this.matches()) {
      this.dialogRef.close(true);
    }
  }

  onCancel(): void {
    this.dialogRef.close(false);
  }
}
