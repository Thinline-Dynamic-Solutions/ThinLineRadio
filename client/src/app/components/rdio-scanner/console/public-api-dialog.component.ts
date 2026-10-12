/*
 * *****************************************************************************
 * Copyright (C) 2026 Thinline Dynamic Solutions
 *
 * Licensed under the GNU GPL v3 (or later). See LICENSE.
 * ****************************************************************************
 */

import { Component, OnInit, ChangeDetectionStrategy, ChangeDetectorRef } from '@angular/core';
import { HttpClient, HttpHeaders } from '@angular/common/http';
import { MatDialogRef } from '@angular/material/dialog';
import { MatSnackBar } from '@angular/material/snack-bar';
import { firstValueFrom } from 'rxjs';
import { RdioScannerService } from '../rdio-scanner.service';

interface PublicApiKeyRow {
  id: number;
  name: string;
  keyPrefix: string;
  createdAt: number;
  lastUsedAt: number;
  disabled: boolean;
  requestCount: number;
}

interface PublicApiWebhookRow {
  id: number;
  destinationType: string;
  url: string;
  email: string;
  enabled: boolean;
  alertTypes: string[];
}

@Component({
  selector: 'rdio-scanner-public-api-dialog',
  templateUrl: './public-api-dialog.component.html',
  styleUrls: ['./public-api-dialog.component.scss'],
  changeDetection: ChangeDetectionStrategy.OnPush,
  standalone: false,
})
export class RdioScannerPublicApiDialogComponent implements OnInit {
  loading = true;
  keys: PublicApiKeyRow[] = [];
  webhooks: PublicApiWebhookRow[] = [];
  capabilities: { health?: boolean; transcripts?: boolean; alertWebhooks?: boolean } = {};
  newKeyName = '';
  createdKeyPlain: string | null = null;
  webhookType = 'slack';
  webhookUrl = '';
  webhookEmail = '';
  webhookSecret = '';

  constructor(
    private dialogRef: MatDialogRef<RdioScannerPublicApiDialogComponent>,
    private http: HttpClient,
    private snack: MatSnackBar,
    private rdioScannerService: RdioScannerService,
    private cdr: ChangeDetectorRef,
  ) {}

  get hasAlertWebhooks(): boolean {
    return !!this.capabilities?.alertWebhooks;
  }

  get healthUrl(): string {
    return `${window.location.origin}/api/public/v1/health`;
  }

  ngOnInit(): void {
    void this.reload();
  }

  private authHeaders(): HttpHeaders {
    const pin = this.rdioScannerService.readPin();
    return pin ? new HttpHeaders({ Authorization: `Bearer ${pin}` }) : new HttpHeaders();
  }

  async reload(): Promise<void> {
    this.loading = true;
    this.cdr.markForCheck();
    try {
      const res = await firstValueFrom(
        this.http.get<any>('/api/public/v1/keys', { headers: this.authHeaders() }),
      );
      this.keys = Array.isArray(res?.keys) ? res.keys : [];
      this.capabilities = res?.capabilities || {};
      if (this.hasAlertWebhooks) {
        const wh = await firstValueFrom(
          this.http.get<any>('/api/public/v1/webhooks', { headers: this.authHeaders() }),
        );
        this.webhooks = Array.isArray(wh?.webhooks) ? wh.webhooks : [];
      } else {
        this.webhooks = [];
      }
    } catch (err: any) {
      this.snack.open(err?.error?.error || 'Failed to load API settings', 'Close', { duration: 4000 });
    } finally {
      this.loading = false;
      this.cdr.markForCheck();
    }
  }

  async createKey(): Promise<void> {
    try {
      const res = await firstValueFrom(
        this.http.post<any>(
          '/api/public/v1/keys',
          { name: this.newKeyName || 'API key' },
          { headers: this.authHeaders() },
        ),
      );
      this.createdKeyPlain = res?.key || null;
      this.newKeyName = '';
      this.snack.open('Key created — copy it now; it will not be shown again.', 'Close', { duration: 5000 });
      await this.reload();
    } catch (err: any) {
      this.snack.open(err?.error?.error || 'Failed to create key', 'Close', { duration: 4000 });
    }
  }

  async revokeKey(id: number): Promise<void> {
    if (!confirm('Revoke this API key?')) {
      return;
    }
    try {
      await firstValueFrom(
        this.http.delete(`/api/public/v1/keys/${id}`, { headers: this.authHeaders() }),
      );
      this.snack.open('Key revoked', 'Close', { duration: 2500 });
      await this.reload();
    } catch (err: any) {
      this.snack.open(err?.error?.error || 'Failed to revoke key', 'Close', { duration: 4000 });
    }
  }

  async createWebhook(): Promise<void> {
    const body: any = {
      destinationType: this.webhookType,
      url: this.webhookUrl,
      email: this.webhookEmail,
      secret: this.webhookSecret,
      enabled: true,
      alertTypes: ['*'],
    };
    try {
      await firstValueFrom(
        this.http.post('/api/public/v1/webhooks', body, { headers: this.authHeaders() }),
      );
      this.webhookUrl = '';
      this.webhookEmail = '';
      this.webhookSecret = '';
      this.snack.open('Webhook saved', 'Close', { duration: 2500 });
      await this.reload();
    } catch (err: any) {
      this.snack.open(err?.error?.error || 'Failed to save webhook', 'Close', { duration: 4000 });
    }
  }

  async deleteWebhook(id: number): Promise<void> {
    if (!confirm('Delete this webhook destination?')) {
      return;
    }
    try {
      await firstValueFrom(
        this.http.delete(`/api/public/v1/webhooks/${id}`, { headers: this.authHeaders() }),
      );
      await this.reload();
    } catch (err: any) {
      this.snack.open(err?.error?.error || 'Failed to delete webhook', 'Close', { duration: 4000 });
    }
  }

  copy(text: string): void {
    void navigator.clipboard.writeText(text);
    this.snack.open('Copied', 'Close', { duration: 1500 });
  }

  close(): void {
    this.dialogRef.close();
  }
}
