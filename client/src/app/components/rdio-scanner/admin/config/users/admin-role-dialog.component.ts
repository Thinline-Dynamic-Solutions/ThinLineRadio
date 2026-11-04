/*
 * *****************************************************************************
 * Copyright (C) 2026 Thinline Dynamic Solutions
 *
 * Licensed under the GNU GPL v3 (or later). See LICENSE.
 * ****************************************************************************
 */

import { ChangeDetectionStrategy, Component, Inject } from '@angular/core';
import { MAT_DIALOG_DATA, MatDialogRef } from '@angular/material/dialog';
import { AdminRole } from '../../admin.service';

export interface AdminRoleDialogData {
    email: string;
    roleId: number;
    systemAdmin: boolean;
    roles: AdminRole[];
}

@Component({
    selector: 'rdio-scanner-admin-role-dialog',
    template: `
    <h2 mat-dialog-title>Admin Role</h2>
    <mat-dialog-content>
      @if (data.systemAdmin) {
        <p class="mat-body">
          <strong>{{ data.email }}</strong> is a system admin and already has the whole admin console.
          A role only matters for users who aren't system admins.
        </p>
      } @else {
        <p class="mat-body">
          <strong>{{ data.email }}</strong> signs in to the admin console with their own account and can use only
          what the role allows. Every change they make is written to Logs → Admin Audit.
        </p>
      }
      @if (!data.roles.length) {
        <p class="mat-body muted">No roles yet. Create one under Admin Roles first.</p>
      } @else {
        <mat-radio-group class="role-options" [(ngModel)]="roleId">
          <mat-radio-button [value]="0">No admin access</mat-radio-button>
          @for (role of data.roles; track role.id) {
            <mat-radio-button [value]="role.id">{{ role.name }}</mat-radio-button>
          }
        </mat-radio-group>
      }
    </mat-dialog-content>
    <mat-dialog-actions align="end">
      <button mat-button mat-dialog-close>Cancel</button>
      <button mat-raised-button color="primary" [disabled]="roleId === data.roleId" (click)="save()">Save</button>
    </mat-dialog-actions>
    `,
    styles: [`
      .role-options { display: flex; flex-direction: column; gap: 6px; margin-top: 8px; }
      .muted { opacity: 0.7; }
    `],
    changeDetection: ChangeDetectionStrategy.Eager,
    standalone: false
})
export class AdminRoleDialogComponent {
    roleId: number;

    constructor(
        @Inject(MAT_DIALOG_DATA) public data: AdminRoleDialogData,
        private dialogRef: MatDialogRef<AdminRoleDialogComponent, number>,
    ) {
        this.roleId = data.roleId || 0;
    }

    save(): void {
        this.dialogRef.close(this.roleId);
    }
}
