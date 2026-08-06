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

import { Component, Inject, OnInit } from '@angular/core';
import { MatDialogRef, MAT_DIALOG_DATA } from '@angular/material/dialog';
import { FormBuilder, FormGroup, Validators } from '@angular/forms';

@Component({
    selector: 'rdio-scanner-transfer-user-dialog',
    template: `
    <h2 mat-dialog-title>Transfer User to Group</h2>
    <mat-dialog-content>
      <p class="mat-body" style="margin-bottom: 16px;">
        Transfer <strong>{{ data.user.email }}</strong> to another group.
      </p>
      <form [formGroup]="transferForm">
        <mat-form-field appearance="outline" class="full-width">
          <mat-label>Target Group</mat-label>
          <mat-select formControlName="groupId" required>
            <mat-option [value]="0">No Group (Unassigned)</mat-option>
            @for (group of availableGroups; track group) {
              <mat-option [value]="group.id">
                {{ group.name }}
              </mat-option>
            }
          </mat-select>
          @if (transferForm.get('groupId')?.hasError('required')) {
            <mat-error>
              Group selection is required
            </mat-error>
          }
        </mat-form-field>
      </form>
    </mat-dialog-content>
    <mat-dialog-actions>
      <button mat-button (click)="onCancel()">Cancel</button>
      <button mat-raised-button color="primary"
        [disabled]="transferForm.invalid"
        (click)="onTransfer()">
        Transfer User
      </button>
    </mat-dialog-actions>
    `,
    styles: [`
    .full-width {
      width: 100%;
      margin-bottom: 16px;
    }
    mat-dialog-content {
      min-width: 400px;
    }
  `],
    standalone: false
})
export class TransferUserDialogComponent implements OnInit {
  transferForm: FormGroup;
  availableGroups: any[] = [];

  constructor(
    public dialogRef: MatDialogRef<TransferUserDialogComponent>,
    @Inject(MAT_DIALOG_DATA) public data: { user: any, groups: any[], currentGroupId: number },
    private fb: FormBuilder
  ) {
    // Filter out the current group from available groups
    this.availableGroups = (data.groups || []).filter(g => g.id !== data.currentGroupId);
    
    this.transferForm = this.fb.group({
      groupId: [0, Validators.required]
    });
  }

  ngOnInit(): void {
    // Pre-select first available group if any
    if (this.availableGroups.length > 0 && !this.transferForm.get('groupId')?.value) {
      this.transferForm.patchValue({ groupId: this.availableGroups[0].id });
    }
  }

  onCancel(): void {
    this.dialogRef.close();
  }

  onTransfer(): void {
    if (this.transferForm.valid) {
      this.dialogRef.close(this.transferForm.value);
    }
  }
}

