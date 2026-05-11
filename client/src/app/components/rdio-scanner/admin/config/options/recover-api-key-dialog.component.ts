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
import { HttpClient } from '@angular/common/http';
import { MatSnackBar } from '@angular/material/snack-bar';

@Component({
  selector: 'rdio-scanner-recover-api-key-dialog',
  template: `
    <h2 mat-dialog-title>Recover API Key</h2>
    <mat-dialog-content>
      <p style="font-size: 13px; color: #666; line-height: 1.45; margin: 0 0 16px 0;">
        This dialog uses your browser’s cryptography APIs. Load admin over HTTPS, or over http://localhost / http://127.0.0.1,
        with the server listening on 0.0.0.0 if you need loopback access. Plain http:// with a LAN IP or hostname often cannot complete this step.
      </p>
      <div *ngIf="!codeSent && !apiKeyRecovered">
        <p style="margin-bottom: 20px; color: #666;">
          Enter your server URL and email address. A verification code will be sent to your email.
        </p>
        <form [formGroup]="recoveryForm">
          <mat-form-field appearance="outline" class="full-width">
            <mat-label>Server URL *</mat-label>
            <input matInput formControlName="serverURL" placeholder="https://your-server.com" required>
            <mat-hint>Your rdio-scanner server address</mat-hint>
            <mat-error *ngIf="recoveryForm.get('serverURL')?.hasError('required')">
              Server URL is required
            </mat-error>
          </mat-form-field>

          <mat-form-field appearance="outline" class="full-width">
            <mat-label>Contact Email *</mat-label>
            <input matInput type="email" formControlName="email" required>
            <mat-hint>The email address used when registering your API key</mat-hint>
            <mat-error *ngIf="recoveryForm.get('email')?.hasError('required')">
              Email is required
            </mat-error>
            <mat-error *ngIf="recoveryForm.get('email')?.hasError('email')">
              Please enter a valid email address
            </mat-error>
          </mat-form-field>

          <div *ngIf="errorMessage" class="error-message">
            {{ errorMessage }}
          </div>

          <div *ngIf="loading" class="loading">
            Sending verification code...
          </div>
        </form>
      </div>

      <div *ngIf="codeSent && !apiKeyRecovered">
        <div class="success-message">
          <mat-icon color="primary">check_circle</mat-icon>
          Verification code sent!
        </div>
        <p style="margin: 20px 0; color: #666;">
          Check your email for the verification code, then enter it below.
        </p>
        <form [formGroup]="verifyForm">
          <mat-form-field appearance="outline" class="full-width">
            <mat-label>Verification Code *</mat-label>
            <input matInput formControlName="code" placeholder="000000" maxlength="6" required>
            <mat-hint>6-digit code from your email</mat-hint>
            <mat-error *ngIf="verifyForm.get('code')?.hasError('required')">
              Verification code is required
            </mat-error>
            <mat-error *ngIf="verifyForm.get('code')?.hasError('pattern')">
              Code must be 6 digits
            </mat-error>
          </mat-form-field>

          <div *ngIf="errorMessage" class="error-message">
            {{ errorMessage }}
          </div>

          <div *ngIf="loading" class="loading">
            Verifying code...
          </div>
        </form>
      </div>

      <div *ngIf="apiKeyRecovered" class="api-key-result">
        <div class="success-message">
          <mat-icon color="primary">check_circle</mat-icon>
          API key recovered successfully!
        </div>
        <div class="api-key-display">
          <label>Your API Key:</label>
          <div class="api-key-value">{{ recoveredApiKey }}</div>
          <button mat-icon-button (click)="copyToClipboard()" matTooltip="Copy to clipboard">
            <mat-icon>content_copy</mat-icon>
          </button>
        </div>
        <p class="warning">
          <mat-icon>warning</mat-icon>
          <strong>Important:</strong> Save this key now - it will not be shown again!
        </p>
        <p class="security-warning">
          <mat-icon>security</mat-icon>
          <strong>Security:</strong> Do not share this API key with anyone. Keep it confidential.
        </p>
      </div>
    </mat-dialog-content>
    <mat-dialog-actions>
      <button mat-button (click)="onCancel()">{{ apiKeyRecovered ? 'Close' : 'Cancel' }}</button>
      <button *ngIf="!codeSent && !apiKeyRecovered" 
              mat-raised-button 
              color="primary" 
              [disabled]="recoveryForm.invalid || loading" 
              (click)="onSendCode()">
        Send Verification Code
      </button>
      <button *ngIf="codeSent && !apiKeyRecovered" 
              mat-raised-button 
              color="primary" 
              [disabled]="verifyForm.invalid || loading" 
              (click)="onVerifyCode()">
        Verify Code
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
      max-width: 600px;
      max-height: 90vh;
      overflow-y: auto;
      padding: 24px !important;
    }
    mat-dialog-title {
      margin: 0;
      padding: 24px 24px 16px 24px;
      font-size: 20px;
      font-weight: 500;
    }
    form {
      display: flex;
      flex-direction: column;
    }
    mat-form-field {
      display: block;
      width: 100%;
    }
    .error-message {
      color: #f44336;
      padding: 10px;
      background: #ffebee;
      border-radius: 4px;
      margin-top: 10px;
    }
    .loading {
      text-align: center;
      padding: 20px;
      color: #666;
    }
    .api-key-result {
      padding: 20px 0;
    }
    .success-message {
      display: flex;
      align-items: center;
      gap: 8px;
      color: #4caf50;
      margin-bottom: 20px;
      font-weight: 500;
    }
    .api-key-display {
      background: #f5f5f5;
      padding: 15px;
      border-radius: 4px;
      margin: 20px 0;
      position: relative;
    }
    .api-key-display label {
      display: block;
      font-size: 12px;
      color: #666;
      margin-bottom: 8px;
    }
    .api-key-value {
      font-family: monospace;
      font-size: 14px;
      word-break: break-all;
      padding-right: 40px;
    }
    .api-key-display button {
      position: absolute;
      top: 15px;
      right: 15px;
    }
    .warning {
      display: flex;
      align-items: center;
      gap: 8px;
      color: #ff9800;
      margin-top: 15px;
      font-size: 14px;
    }
    .warning mat-icon {
      font-size: 20px;
      width: 20px;
      height: 20px;
    }
    .security-warning {
      display: flex;
      align-items: center;
      gap: 8px;
      color: #f44336;
      margin-top: 10px;
      font-size: 14px;
      font-weight: 500;
    }
    .security-warning mat-icon {
      font-size: 20px;
      width: 20px;
      height: 20px;
    }
  `]
})
export class RecoverAPIKeyDialogComponent implements OnInit {
  recoveryForm: FormGroup;
  verifyForm: FormGroup;
  loading = false;
  errorMessage = '';
  codeSent = false;
  apiKeyRecovered = false;
  recoveredApiKey = '';
  authKey: string = '';
  serverURL = '';
  email = '';

  constructor(
    public dialogRef: MatDialogRef<RecoverAPIKeyDialogComponent>,
    @Inject(MAT_DIALOG_DATA) public data: { relayServerURL: string },
    private fb: FormBuilder,
    private http: HttpClient,
    private snackBar: MatSnackBar
  ) {
    this.recoveryForm = this.fb.group({
      serverURL: ['', [Validators.required]],
      email: ['', [Validators.required, Validators.email]]
    });

    this.verifyForm = this.fb.group({
      code: ['', [Validators.required, Validators.pattern(/^\d{6}$/)]]
    });
  }

  async ngOnInit(): Promise<void> {
    // Compute hash using Web Crypto API (same algorithm as backend SHA256)
    const seed = 'thinline-radio-relay-auth-2026';
    const encoder = new TextEncoder();
    const data = encoder.encode(seed);
    
    try {
      const hashBuffer = await crypto.subtle.digest('SHA-256', data);
      const hashArray = Array.from(new Uint8Array(hashBuffer));
      this.authKey = hashArray.map(b => b.toString(16).padStart(2, '0')).join('');
    } catch (error) {
      console.error('Failed to compute auth key:', error);
      this.errorMessage = 'Failed to initialize authorization. Please refresh the page.';
    }
  }

  async onSendCode(): Promise<void> {
    if (this.recoveryForm.invalid) {
      return;
    }

    this.loading = true;
    this.errorMessage = '';
    this.serverURL = this.recoveryForm.get('serverURL')?.value.trim();
    this.email = this.recoveryForm.get('email')?.value.trim();

    try {
      if (!this.authKey) {
        this.errorMessage = 'Failed to get authorization key. Please try again.';
        this.loading = false;
        return;
      }

      const headers = {
        'Content-Type': 'application/json',
        'X-Rdio-Auth': this.authKey
      };

      const payload = {
        server_url: this.serverURL,
        email: this.email
      };

      const response: any = await this.http.post(`${this.data.relayServerURL}/api/keys/recover/initiate`, payload, { headers }).toPromise();

      if (response && response.success) {
        this.codeSent = true;
      } else {
        this.errorMessage = response?.message || 'Failed to send verification code';
      }
    } catch (error: any) {
      console.error('Error sending recovery code:', error);
      const message = error.error?.error || error.message || 'Failed to send verification code';
      this.errorMessage = `Error: ${message}`;
    } finally {
      this.loading = false;
    }
  }

  async onVerifyCode(): Promise<void> {
    if (this.verifyForm.invalid) {
      return;
    }

    this.loading = true;
    this.errorMessage = '';

    try {
      if (!this.authKey) {
        this.errorMessage = 'Failed to get authorization key. Please try again.';
        this.loading = false;
        return;
      }

      const headers = {
        'Content-Type': 'application/json',
        'X-Rdio-Auth': this.authKey
      };

      const payload = {
        server_url: this.serverURL,
        email: this.email,
        code: this.verifyForm.get('code')?.value
      };

      const response: any = await this.http.post(`${this.data.relayServerURL}/api/keys/recover/verify`, payload, { headers }).toPromise();

      if (response && response.success && response.api_key) {
        this.recoveredApiKey = response.api_key;
        this.apiKeyRecovered = true;
        // Auto-close after 3 seconds
        setTimeout(() => {
          this.dialogRef.close(this.recoveredApiKey);
        }, 3000);
      } else {
        this.errorMessage = response?.message || 'Invalid verification code';
      }
    } catch (error: any) {
      console.error('Error verifying recovery code:', error);
      const message = error.error?.error || error.message || 'Failed to verify code';
      this.errorMessage = `Error: ${message}`;
    } finally {
      this.loading = false;
    }
  }

  copyToClipboard(): void {
    if (!this.recoveredApiKey) return;
    
    navigator.clipboard.writeText(this.recoveredApiKey).then(() => {
      this.snackBar.open('API key copied to clipboard', 'Close', { duration: 3000 });
    }).catch(err => {
      console.error('Failed to copy:', err);
      this.snackBar.open('Failed to copy API key. Please copy manually.', 'Close', { duration: 5000 });
    });
  }

  onCancel(): void {
    this.dialogRef.close(this.apiKeyRecovered ? this.recoveredApiKey : null);
  }
}

