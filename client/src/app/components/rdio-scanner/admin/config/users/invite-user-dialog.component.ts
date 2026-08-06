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

// Results Dialog Component
@Component({
    selector: 'rdio-scanner-invitation-results-dialog',
    template: `
    <h2 mat-dialog-title>
      <mat-icon [style.color]="data.failureCount > 0 ? '#f44336' : '#4caf50'">
        {{ data.failureCount > 0 ? 'warning' : 'check_circle' }}
      </mat-icon>
      Invitation Results
    </h2>
    <mat-dialog-content>
      <div class="results-summary">
        <div class="success-count" *ngIf="data.successCount > 0">
          <mat-icon>check_circle</mat-icon>
          <span>{{ data.successCount }} invitation{{ data.successCount !== 1 ? 's' : '' }} sent successfully</span>
        </div>
        <div class="failure-count" *ngIf="data.failureCount > 0">
          <mat-icon>error</mat-icon>
          <span>{{ data.failureCount }} invitation{{ data.failureCount !== 1 ? 's' : '' }} failed</span>
        </div>
      </div>

      <div class="results-list">
        <div *ngFor="let result of data.results" class="result-item" [class.success]="result.success" [class.failure]="!result.success">
          <mat-icon>{{ result.success ? 'check_circle' : 'error' }}</mat-icon>
          <div class="result-details">
            <div class="email">{{ result.email }}</div>
            <div class="message">{{ result.message }}</div>
          </div>
        </div>
      </div>
    </mat-dialog-content>
    <mat-dialog-actions>
      <button mat-raised-button color="primary" (click)="onClose()">Close</button>
    </mat-dialog-actions>
  `,
    styles: [`
    mat-dialog-title {
      display: flex;
      align-items: center;
      gap: 12px;
      margin: 0;
      padding: 24px 24px 16px 24px;
      font-size: 20px;
      font-weight: 500;

      mat-icon {
        font-size: 28px;
        width: 28px;
        height: 28px;
      }
    }
    mat-dialog-content {
      min-width: 500px;
      max-height: 500px;
      padding: 0 24px 24px 24px !important;
      overflow-y: auto;
    }
    .results-summary {
      display: flex;
      flex-direction: column;
      gap: 12px;
      margin-bottom: 24px;
      padding: 16px;
      background: #f5f5f5;
      border-radius: 8px;

      .success-count, .failure-count {
        display: flex;
        align-items: center;
        gap: 8px;
        font-weight: 500;

        mat-icon {
          font-size: 20px;
          width: 20px;
          height: 20px;
        }
      }

      .success-count {
        color: #4caf50;
      }

      .failure-count {
        color: #f44336;
      }
    }
    .results-list {
      display: flex;
      flex-direction: column;
      gap: 8px;

      .result-item {
        display: flex;
        align-items: flex-start;
        gap: 12px;
        padding: 12px;
        border-radius: 6px;
        border: 1px solid;

        mat-icon {
          font-size: 20px;
          width: 20px;
          height: 20px;
          margin-top: 2px;
        }

        &.success {
          background: #e8f5e9;
          border-color: #4caf50;

          mat-icon {
            color: #4caf50;
          }
        }

        &.failure {
          background: #ffebee;
          border-color: #f44336;

          mat-icon {
            color: #f44336;
          }
        }

        .result-details {
          flex: 1;

          .email {
            font-weight: 500;
            margin-bottom: 4px;
          }

          .message {
            font-size: 13px;
            color: #666;
          }
        }
      }
    }
    mat-dialog-actions {
      padding: 16px 24px;
      justify-content: flex-end;
    }
  `],
    standalone: false
})
export class InvitationResultsDialogComponent {
  constructor(
    public dialogRef: MatDialogRef<InvitationResultsDialogComponent>,
    @Inject(MAT_DIALOG_DATA) public data: {
      results: Array<{ email: string; success: boolean; message: string }>;
      successCount: number;
      failureCount: number;
    }
  ) {}

  onClose(): void {
    this.dialogRef.close();
  }
}

@Component({
    selector: 'rdio-scanner-invite-user-dialog',
    template: `
    <h2 mat-dialog-title>Invite Users</h2>
    <mat-dialog-content>
      <form [formGroup]="inviteForm">
        <mat-form-field appearance="outline" class="full-width">
          <mat-label>Email Addresses</mat-label>
          <textarea matInput 
                    formControlName="emails" 
                    rows="6" 
                    placeholder="Enter email addresses (one per line or comma-separated)"
                    required></textarea>
          <mat-hint>Enter one or multiple email addresses (one per line or comma-separated)</mat-hint>
          <mat-error *ngIf="inviteForm.get('emails')?.hasError('required')">
            At least one email is required
          </mat-error>
          <mat-error *ngIf="inviteForm.get('emails')?.hasError('invalidEmails')">
            {{ getInvalidEmailsError() }}
          </mat-error>
        </mat-form-field>

        <div class="email-count" *ngIf="getEmailCount() > 0">
          <mat-icon>mail</mat-icon>
          <span>{{ getEmailCount() }} email{{ getEmailCount() !== 1 ? 's' : '' }} to invite</span>
        </div>

        <mat-form-field appearance="outline" class="full-width">
          <mat-label>User Group</mat-label>
          <mat-select formControlName="groupId" required>
            <mat-option *ngFor="let group of data.groups" [value]="group.id">
              {{ group.name }}
            </mat-option>
          </mat-select>
          <mat-error *ngIf="inviteForm.get('groupId')?.hasError('required')">
            Group is required
          </mat-error>
        </mat-form-field>
      </form>
    </mat-dialog-content>
    <mat-dialog-actions>
      <button mat-button (click)="onCancel()">Cancel</button>
      <button mat-raised-button color="primary" 
              [disabled]="inviteForm.invalid" 
              (click)="onInvite()">
        Send {{ getEmailCount() > 1 ? getEmailCount() + ' Invitations' : 'Invitation' }}
      </button>
    </mat-dialog-actions>
  `,
    styles: [`
    .full-width {
      width: 100%;
      margin-bottom: 16px;
    }
    mat-dialog-content {
      min-width: 500px;
      padding: 24px !important;
      overflow: visible;
    }
    mat-dialog-title {
      margin: 0;
      padding: 24px 24px 16px 24px;
      font-size: 20px;
      font-weight: 500;
    }
    mat-form-field {
      display: block;
      width: 100%;
    }
    textarea {
      font-family: inherit;
      line-height: 1.5;
    }
    .email-count {
      display: flex;
      align-items: center;
      gap: 8px;
      padding: 12px;
      background: #e3f2fd;
      border-radius: 4px;
      margin-bottom: 16px;
      color: #1976d2;
      font-size: 14px;
      font-weight: 500;

      mat-icon {
        font-size: 20px;
        width: 20px;
        height: 20px;
      }
    }
    ::ng-deep .mat-mdc-form-field {
      .mat-mdc-text-field-wrapper {
        padding-bottom: 0;
      }
      .mat-mdc-form-field-subscript-wrapper {
        margin-top: 4px;
      }
    }
  `],
    standalone: false
})
export class InviteUserDialogComponent implements OnInit {
  inviteForm: FormGroup;

  constructor(
    public dialogRef: MatDialogRef<InviteUserDialogComponent>,
    @Inject(MAT_DIALOG_DATA) public data: { groups: any[] },
    private fb: FormBuilder
  ) {
    this.inviteForm = this.fb.group({
      emails: ['', [Validators.required, this.emailsValidator.bind(this)]],
      groupId: [null, Validators.required]
    });
  }

  ngOnInit(): void {
    if (this.data.groups && this.data.groups.length > 0 && !this.inviteForm.get('groupId')?.value) {
      this.inviteForm.patchValue({ groupId: this.data.groups[0].id });
    }
  }

  emailsValidator(control: any) {
    if (!control.value) {
      return null;
    }

    const emails = this.parseEmails(control.value);
    const emailRegex = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;
    const invalidEmails = emails.filter(email => !emailRegex.test(email));

    if (invalidEmails.length > 0) {
      return { invalidEmails: invalidEmails };
    }

    return null;
  }

  parseEmails(input: string): string[] {
    if (!input || !input.trim()) {
      return [];
    }

    // Split by newlines and commas, then trim and filter empty strings
    return input
      .split(/[\n,]+/)
      .map(email => email.trim())
      .filter(email => email.length > 0);
  }

  getEmailCount(): number {
    const emailsControl = this.inviteForm.get('emails');
    if (!emailsControl || !emailsControl.value) {
      return 0;
    }
    return this.parseEmails(emailsControl.value).length;
  }

  getInvalidEmailsError(): string {
    const errors = this.inviteForm.get('emails')?.errors;
    if (errors && errors['invalidEmails']) {
      const invalidEmails = errors['invalidEmails'];
      if (invalidEmails.length === 1) {
        return `Invalid email: ${invalidEmails[0]}`;
      } else if (invalidEmails.length <= 3) {
        return `Invalid emails: ${invalidEmails.join(', ')}`;
      } else {
        return `${invalidEmails.length} invalid email addresses`;
      }
    }
    return '';
  }

  onCancel(): void {
    this.dialogRef.close();
  }

  onInvite(): void {
    if (this.inviteForm.valid) {
      const emails = this.parseEmails(this.inviteForm.value.emails);
      this.dialogRef.close({
        emails: emails,
        groupId: this.inviteForm.value.groupId
      });
    }
  }
}

// Create User Dialog Component
@Component({
    selector: 'rdio-scanner-create-user-dialog',
    template: `
    <h2 mat-dialog-title>
      <mat-icon>person_add</mat-icon>
      Create New User
    </h2>
    <mat-dialog-content>
      <form [formGroup]="createForm">
        <mat-form-field appearance="outline" class="full-width">
          <mat-label>Email</mat-label>
          <input matInput formControlName="email" type="email" required>
          <mat-error *ngIf="createForm.get('email')?.hasError('required')">
            Email is required
          </mat-error>
          <mat-error *ngIf="createForm.get('email')?.hasError('email')">
            Invalid email format
          </mat-error>
        </mat-form-field>

        <mat-form-field appearance="outline" class="full-width">
          <mat-label>Password</mat-label>
          <input matInput formControlName="password" type="password" required>
          <mat-error *ngIf="createForm.get('password')?.hasError('required')">
            Password is required
          </mat-error>
          <mat-error *ngIf="createForm.get('password')?.hasError('minlength')">
            Password must be at least 6 characters
          </mat-error>
        </mat-form-field>

        <mat-form-field appearance="outline" class="full-width">
          <mat-label>First Name</mat-label>
          <input matInput formControlName="firstName">
        </mat-form-field>

        <mat-form-field appearance="outline" class="full-width">
          <mat-label>Last Name</mat-label>
          <input matInput formControlName="lastName">
        </mat-form-field>

        <mat-form-field appearance="outline" class="full-width">
          <mat-label>Zip Code</mat-label>
          <input matInput formControlName="zipCode">
        </mat-form-field>

        <mat-form-field appearance="outline" class="full-width">
          <mat-label>User Group</mat-label>
          <mat-select formControlName="userGroupId">
            <mat-option [value]="0">No Group</mat-option>
            <mat-option *ngFor="let group of data.groups" [value]="group.id">
              {{ group.name }}
            </mat-option>
          </mat-select>
        </mat-form-field>

        <mat-checkbox formControlName="verified" class="full-width">
          Mark as verified (user won't need to verify email)
        </mat-checkbox>
      </form>
    </mat-dialog-content>
    <mat-dialog-actions>
      <button mat-button (click)="onCancel()">Cancel</button>
      <button mat-raised-button color="primary" (click)="onCreate()" [disabled]="!createForm.valid">
        Create User
      </button>
    </mat-dialog-actions>
  `,
    styles: [`
    mat-dialog-title {
      display: flex;
      align-items: center;
      gap: 12px;
      margin: 0;
      padding: 24px 24px 16px 24px;
      font-size: 20px;
      font-weight: 500;

      mat-icon {
        font-size: 28px;
        width: 28px;
        height: 28px;
      }
    }
    mat-dialog-content {
      min-width: 450px;
      padding: 0 24px 24px 24px !important;
    }
    .full-width {
      width: 100%;
      display: block;
      margin-bottom: 16px;
    }
    mat-dialog-actions {
      padding: 16px 24px;
      justify-content: flex-end;
      gap: 8px;
    }
  `],
    standalone: false
})
export class CreateUserDialogComponent implements OnInit {
  createForm: FormGroup;

  constructor(
    public dialogRef: MatDialogRef<CreateUserDialogComponent>,
    @Inject(MAT_DIALOG_DATA) public data: { groups: any[] },
    private fb: FormBuilder
  ) {
    this.createForm = this.fb.group({
      email: ['', [Validators.required, Validators.email]],
      password: ['', [Validators.required, Validators.minLength(6)]],
      firstName: [''],
      lastName: [''],
      zipCode: [''],
      userGroupId: [0],
      verified: [true]
    });
  }

  ngOnInit(): void {
    // Additional initialization if needed
  }

  onCancel(): void {
    this.dialogRef.close();
  }

  onCreate(): void {
    if (this.createForm.valid) {
      this.dialogRef.close(this.createForm.value);
    }
  }
}

// Reset Password Dialog Component
@Component({
    selector: 'rdio-scanner-reset-password-dialog',
    template: `
    <h2 mat-dialog-title>
      <mat-icon>lock_reset</mat-icon>
      Reset User Password
    </h2>
    <mat-dialog-content>
      <p class="mat-body">
        Reset password for user: <strong>{{ data.userEmail }}</strong>
      </p>
      <form [formGroup]="resetForm">
        <mat-form-field appearance="outline" class="full-width">
          <mat-label>New Password</mat-label>
          <input matInput formControlName="newPassword" type="password" required>
          <mat-error *ngIf="resetForm.get('newPassword')?.hasError('required')">
            Password is required
          </mat-error>
          <mat-error *ngIf="resetForm.get('newPassword')?.hasError('minlength')">
            Password must be at least 6 characters
          </mat-error>
        </mat-form-field>

        <mat-form-field appearance="outline" class="full-width">
          <mat-label>Confirm New Password</mat-label>
          <input matInput formControlName="confirmPassword" type="password" required>
          <mat-error *ngIf="resetForm.get('confirmPassword')?.hasError('required')">
            Confirm password is required
          </mat-error>
          <mat-error *ngIf="resetForm.hasError('passwordMismatch') && resetForm.get('confirmPassword')?.touched">
            Passwords do not match
          </mat-error>
        </mat-form-field>
      </form>
    </mat-dialog-content>
    <mat-dialog-actions>
      <button mat-button (click)="onCancel()">Cancel</button>
      <button mat-raised-button color="primary" (click)="onReset()" [disabled]="!resetForm.valid">
        Reset Password
      </button>
    </mat-dialog-actions>
  `,
    styles: [`
    mat-dialog-title {
      display: flex;
      align-items: center;
      gap: 12px;
      margin: 0;
      padding: 24px 24px 16px 24px;
      font-size: 20px;
      font-weight: 500;

      mat-icon {
        font-size: 28px;
        width: 28px;
        height: 28px;
      }
    }
    mat-dialog-content {
      min-width: 400px;
      padding: 0 24px 24px 24px !important;

      p {
        margin-bottom: 16px;
      }
    }
    .full-width {
      width: 100%;
      display: block;
      margin-bottom: 16px;
    }
    mat-dialog-actions {
      padding: 16px 24px;
      justify-content: flex-end;
      gap: 8px;
    }
  `],
    standalone: false
})
export class ResetPasswordDialogComponent implements OnInit {
  resetForm: FormGroup;

  constructor(
    public dialogRef: MatDialogRef<ResetPasswordDialogComponent>,
    @Inject(MAT_DIALOG_DATA) public data: { userId: number, userEmail: string },
    private fb: FormBuilder
  ) {
    this.resetForm = this.fb.group({
      newPassword: ['', [Validators.required, Validators.minLength(6)]],
      confirmPassword: ['', [Validators.required]]
    }, { validators: this.passwordMatchValidator });
  }

  ngOnInit(): void {
    // Additional initialization if needed
  }

  passwordMatchValidator(form: FormGroup) {
    const newPassword = form.get('newPassword');
    const confirmPassword = form.get('confirmPassword');
    
    if (newPassword && confirmPassword && newPassword.value !== confirmPassword.value) {
      return { passwordMismatch: true };
    }
    return null;
  }

  onCancel(): void {
    this.dialogRef.close();
  }

  onReset(): void {
    if (this.resetForm.valid) {
      this.dialogRef.close({ newPassword: this.resetForm.value.newPassword });
    }
  }
}

