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

import { Component, Inject, ChangeDetectionStrategy } from '@angular/core';
import { MatDialogRef, MAT_DIALOG_DATA } from '@angular/material/dialog';
import { HttpClient, HttpHeaders } from '@angular/common/http';

type RelayAccountMode = 'login' | 'create';

@Component({
    selector: 'rdio-scanner-relay-account-dialog',
    template: `
    <h2 mat-dialog-title>Relay Account</h2>
    <mat-dialog-content>
      <p style="font-size: 13px; color: #666; line-height: 1.45; margin: 0 0 16px 0;">
        Create or sign in to the relay account linked to this server's API key.
        Accounts are created here in Thinline Radio admin — not on the public web portal.
      </p>
    
      <div style="display: flex; gap: 8px; margin-bottom: 20px;">
        <button mat-stroked-button type="button" [color]="mode === 'login' ? 'primary' : undefined" (click)="mode = 'login'" style="flex: 1;">
          Sign In
        </button>
        <button mat-stroked-button type="button" [color]="mode === 'create' ? 'primary' : undefined" (click)="mode = 'create'" style="flex: 1;">
          Create Account
        </button>
      </div>
    
      @if (mode === 'login') {
        <form>
          <mat-form-field appearance="outline" class="full-width">
            <mat-label>Email</mat-label>
            <input matInput type="email" [(ngModel)]="email" name="loginEmail" autocomplete="username" [disabled]="loading">
          </mat-form-field>
          <mat-form-field appearance="outline" class="full-width">
            <mat-label>Password</mat-label>
            <input matInput type="password" [(ngModel)]="password" name="password" autocomplete="current-password" [disabled]="loading" (keyup.enter)="onLogin()">
          </mat-form-field>
        </form>
      }
    
      @if (mode === 'create') {
        <form>
          <p style="font-size: 13px; color: #666; margin: 0 0 12px 0; line-height: 1.45;">
            Use the <strong>same email address</strong> you entered when you created your API key.
            That contact email is required and must match exactly.
          </p>
          <mat-form-field appearance="outline" class="full-width">
            <mat-label>Email</mat-label>
            <input matInput type="email" [(ngModel)]="email" name="createEmail" autocomplete="email" [disabled]="loading">
            <mat-hint>Must match the email used when requesting your API key</mat-hint>
          </mat-form-field>
          <mat-form-field appearance="outline" class="full-width">
            <mat-label>Password</mat-label>
            <input matInput type="password" [(ngModel)]="password" name="createPassword" autocomplete="new-password" [disabled]="loading" (keyup.enter)="onCreate()">
          </mat-form-field>
          <mat-form-field appearance="outline" class="full-width">
            <mat-label>Confirm Password</mat-label>
            <input matInput type="password" [(ngModel)]="passwordConfirm" name="createPasswordConfirm" autocomplete="new-password" [disabled]="loading" (keyup.enter)="onCreate()">
          </mat-form-field>
        </form>
      }
    
      @if (errorMessage) {
        <div class="error-message">{{ errorMessage }}</div>
      }
      @if (infoMessage) {
        <div class="info-message">{{ infoMessage }}</div>
      }
    </mat-dialog-content>
    <mat-dialog-actions>
      <button mat-button (click)="onCancel()">Cancel</button>
      @if (mode === 'login') {
        <button mat-raised-button color="primary" [disabled]="!email || !password || loading" (click)="onLogin()">
          {{ loading ? 'Signing In…' : 'Sign In' }}
        </button>
      }
      @if (mode === 'create') {
        <button mat-raised-button color="primary" [disabled]="!canCreate || loading" (click)="onCreate()">
          {{ loading ? 'Creating…' : 'Create Account' }}
        </button>
      }
    </mat-dialog-actions>
    `,
    styles: [`
    .full-width { width: 100%; margin-bottom: 8px; display: block; }
    mat-dialog-content { min-width: 420px; max-width: 480px; }
    .error-message {
      color: #f44336;
      padding: 10px;
      background: #ffebee;
      border-radius: 4px;
      margin-top: 12px;
      font-size: 13px;
    }
    .info-message {
      color: #1976d2;
      padding: 10px;
      background: #e7f3ff;
      border-radius: 4px;
      margin-top: 12px;
      font-size: 13px;
    }
  `],
    changeDetection: ChangeDetectionStrategy.Eager,
    standalone: false
})
export class RelayAccountDialogComponent {
  mode: RelayAccountMode = 'create';
  email = '';
  password = '';
  passwordConfirm = '';
  loading = false;
  errorMessage = '';
  infoMessage = '';

  constructor(
    public dialogRef: MatDialogRef<RelayAccountDialogComponent>,
    @Inject(MAT_DIALOG_DATA) public data: { adminToken: string; initialMode?: RelayAccountMode },
    private http: HttpClient,
  ) {
    if (data?.initialMode === 'login' || data?.initialMode === 'create') {
      this.mode = data.initialMode;
    }
  }

  get canCreate(): boolean {
    return !!(
      this.email &&
      this.email.includes('@') &&
      this.password &&
      this.password.length >= 8 &&
      this.password === this.passwordConfirm
    );
  }

  private get headers(): HttpHeaders {
    return new HttpHeaders({ Authorization: this.data.adminToken });
  }

  onCancel(): void {
    this.dialogRef.close(false);
  }

  onLogin(): void {
    if (!this.email || !this.password) return;
    this.loading = true;
    this.errorMessage = '';
    this.infoMessage = '';
    this.http
      .post<{ success?: boolean; error?: string }>(
        `${window.location.origin}/api/admin/relay-account/login`,
        { username: this.email.trim(), password: this.password },
        { headers: this.headers },
      )
      .subscribe({
        next: (res: any) => {
          this.loading = false;
          if (res?.error) {
            this.errorMessage = res.error;
            return;
          }
          this.dialogRef.close(true);
        },
        error: (err) => {
          this.loading = false;
          this.errorMessage = err?.error?.error || err?.message || 'Sign-in failed';
        },
      });
  }

  onCreate(): void {
    if (!this.canCreate) {
      if (this.password !== this.passwordConfirm) {
        this.errorMessage = 'Passwords do not match';
      } else if (this.password.length < 8) {
        this.errorMessage = 'Password must be at least 8 characters';
      } else if (!this.email.includes('@')) {
        this.errorMessage = 'Enter the email you used when creating your API key';
      }
      return;
    }
    this.loading = true;
    this.errorMessage = '';
    this.infoMessage = '';
    this.http
      .post<{ success?: boolean; error?: string }>(
        `${window.location.origin}/api/admin/relay-account/create`,
        { email: this.email.trim(), password: this.password },
        { headers: this.headers },
      )
      .subscribe({
        next: (res: any) => {
          this.loading = false;
          if (res?.error) {
            this.handleCreateError(String(res.error));
            return;
          }
          this.dialogRef.close(true);
        },
        error: (err) => {
          this.loading = false;
          this.handleCreateError(err?.error?.error || err?.message || 'Failed to create account');
        },
      });
  }

  /** When the API key is already linked, Create is wrong — steer them to Sign In. */
  private handleCreateError(message: string): void {
    const lower = (message || '').toLowerCase();
    if (lower.includes('already has a linked account') || lower.includes('account with that email already exists')) {
      this.mode = 'login';
      this.errorMessage = '';
      this.infoMessage =
        'This server’s API key already has a relay account. Use Sign In with the email that created it ' +
        '(the contact email on the API key — not a different address). ' +
        'If you forgot the password, use password reset on the Thinline portal.';
      return;
    }
    this.errorMessage = message;
  }
}
