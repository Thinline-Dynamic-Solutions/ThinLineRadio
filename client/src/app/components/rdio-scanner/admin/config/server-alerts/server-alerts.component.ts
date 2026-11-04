/*
 * *****************************************************************************
 * Copyright (C) 2026 Thinline Dynamic Solutions
 *
 * Licensed under the GNU GPL v3 (or later). See LICENSE.
 * ****************************************************************************
 */

import { ChangeDetectionStrategy, ChangeDetectorRef, Component, OnInit } from '@angular/core';
import { HttpClient } from '@angular/common/http';
import { FormBuilder, FormGroup, Validators } from '@angular/forms';
import { MatSnackBar } from '@angular/material/snack-bar';
import { RdioScannerAdminService } from '../../admin.service';

type DestinationType = 'slack' | 'discord' | 'pagerduty' | 'email' | 'custom';

interface Destination {
  id: number;
  label: string;
  destinationType: DestinationType;
  url: string;
  email: string;
  hasSecret: boolean;
  minSeverity: string;
  enabled: boolean;
  createdAt: number;
}

interface AlertKey {
  id: number;
  label: string;
  prefix: string;
  enabled: boolean;
  createdAt: number;
  lastUsedAt: number;
}

interface ServerAlertsState {
  destinations: Destination[];
  keys: AlertKey[];
  diskSpaceAlertPercent: number;
  healthAlertsEnabled: boolean;
  emailAvailable: boolean;
}

@Component({
  selector: 'rdio-scanner-admin-server-alerts',
  templateUrl: './server-alerts.component.html',
  styleUrls: ['./server-alerts.component.scss'],
  changeDetection: ChangeDetectionStrategy.OnPush,
  standalone: false,
})
export class RdioScannerAdminServerAlertsComponent implements OnInit {
  readonly types: { value: DestinationType; label: string }[] = [
    { value: 'slack', label: 'Slack' },
    { value: 'discord', label: 'Discord' },
    { value: 'pagerduty', label: 'PagerDuty' },
    { value: 'email', label: 'Email' },
    { value: 'custom', label: 'Custom (JSON POST)' },
  ];
  readonly severities = ['info', 'warning', 'error', 'critical'];

  state: ServerAlertsState | null = null;
  loading = false;
  saving = false;
  testingId = 0;
  showForm = false;
  editing: Destination | null = null;
  form: FormGroup;
  diskPercent = 10;
  newKeyLabel = '';
  newKeyPlaintext = '';

  constructor(
    private http: HttpClient,
    private fb: FormBuilder,
    private adminService: RdioScannerAdminService,
    private snackBar: MatSnackBar,
    private cdr: ChangeDetectorRef,
  ) {
    this.form = this.fb.group({
      label: ['', Validators.required],
      destinationType: ['slack' as DestinationType, Validators.required],
      url: [''],
      email: [''],
      secret: [''],
      minSeverity: ['warning'],
      enabled: [true],
    });
  }

  ngOnInit(): void {
    this.load();
  }

  get ingestUrl(): string {
    return `${window.location.origin}/api/server-alerts`;
  }

  get curlExample(): string {
    return `curl -X POST ${this.ingestUrl} \\
  -H "Authorization: Bearer tla_..." \\
  -H "Content-Type: application/json" \\
  -d '{"source":"trunk-recorder","severity":"critical","title":"Recorder down","message":"trunk-recorder stopped on tr-1","metadata":{"host":"tr-1"}}'`;
  }

  get formType(): DestinationType {
    return this.form.get('destinationType')?.value;
  }

  typeLabel(type: string): string {
    return this.types.find(t => t.value === type)?.label || type;
  }

  destinationTarget(d: Destination): string {
    if (d.destinationType === 'email') {
      return d.email;
    }
    if (d.destinationType === 'pagerduty') {
      return 'Events API v2';
    }
    try {
      return new URL(d.url).host;
    } catch {
      return d.url;
    }
  }

  load(): void {
    this.loading = true;
    this.http.get<ServerAlertsState>('/api/admin/server-alerts', { headers: this.adminService.getAuthHeaders() }).subscribe({
      next: (res) => {
        this.state = res;
        this.diskPercent = res.diskSpaceAlertPercent;
        this.loading = false;
        this.cdr.markForCheck();
      },
      error: (err) => {
        this.loading = false;
        this.snackBar.open(err.error?.error || 'Could not load server alerts', 'Close', { duration: 4000 });
        this.cdr.markForCheck();
      },
    });
  }

  saveDiskPercent(): void {
    const pct = Math.round(Number(this.diskPercent));
    this.http.put('/api/admin/server-alerts/settings', { diskSpaceAlertPercent: pct }, { headers: this.adminService.getAuthHeaders() }).subscribe({
      next: () => {
        this.snackBar.open(pct ? `Disk space alerts start below ${pct}% free` : 'Disk space alerts are off', 'Close', { duration: 3000 });
        this.load();
      },
      error: (err) => this.snackBar.open(err.error?.error || 'Could not save', 'Close', { duration: 4000 }),
    });
  }

  startCreate(): void {
    this.editing = null;
    this.form.reset({ label: '', destinationType: 'slack', url: '', email: '', secret: '', minSeverity: 'warning', enabled: true });
    this.showForm = true;
    this.cdr.markForCheck();
  }

  startEdit(d: Destination): void {
    this.editing = d;
    this.form.reset({
      label: d.label,
      destinationType: d.destinationType,
      url: d.url,
      email: d.email,
      secret: '',
      minSeverity: d.minSeverity,
      enabled: d.enabled,
    });
    this.showForm = true;
    this.cdr.markForCheck();
  }

  cancel(): void {
    this.showForm = false;
    this.editing = null;
    this.cdr.markForCheck();
  }

  save(): void {
    if (this.form.invalid) {
      this.form.markAllAsTouched();
      return;
    }
    const v = this.form.value;
    const body: Record<string, unknown> = {
      label: (v.label || '').trim(),
      destinationType: v.destinationType,
      url: (v.url || '').trim(),
      email: (v.email || '').trim(),
      minSeverity: v.minSeverity,
      enabled: !!v.enabled,
    };
    // A blank secret on edit keeps the saved one.
    if ((v.secret || '').trim() || !this.editing) {
      body['secret'] = (v.secret || '').trim();
    }
    const headers = this.adminService.getAuthHeaders();
    const req = this.editing
      ? this.http.put(`/api/admin/server-alerts/destinations/${this.editing.id}`, body, { headers })
      : this.http.post('/api/admin/server-alerts/destinations', body, { headers });
    this.saving = true;
    req.subscribe({
      next: () => {
        this.saving = false;
        this.showForm = false;
        this.editing = null;
        this.load();
      },
      error: (err) => {
        this.saving = false;
        this.snackBar.open(err.error?.error || 'Could not save the destination', 'Close', { duration: 5000 });
        this.cdr.markForCheck();
      },
    });
  }

  toggleDestination(d: Destination): void {
    const body = { label: d.label, destinationType: d.destinationType, url: d.url, email: d.email, minSeverity: d.minSeverity, enabled: !d.enabled };
    this.http.put(`/api/admin/server-alerts/destinations/${d.id}`, body, { headers: this.adminService.getAuthHeaders() }).subscribe({
      next: () => this.load(),
      error: (err) => this.snackBar.open(err.error?.error || 'Could not update the destination', 'Close', { duration: 4000 }),
    });
  }

  removeDestination(d: Destination): void {
    if (!confirm(`Delete "${d.label}"? Server alerts stop going there.`)) {
      return;
    }
    this.http.delete(`/api/admin/server-alerts/destinations/${d.id}`, { headers: this.adminService.getAuthHeaders() }).subscribe({
      next: () => this.load(),
      error: (err) => this.snackBar.open(err.error?.error || 'Could not delete the destination', 'Close', { duration: 4000 }),
    });
  }

  test(d: Destination): void {
    this.testingId = d.id;
    this.cdr.markForCheck();
    this.http.post(`/api/admin/server-alerts/destinations/${d.id}/test`, {}, { headers: this.adminService.getAuthHeaders() }).subscribe({
      next: () => {
        this.testingId = 0;
        this.snackBar.open(`Test alert sent to ${d.label}`, 'Close', { duration: 3000 });
        this.cdr.markForCheck();
      },
      error: (err) => {
        this.testingId = 0;
        this.snackBar.open(`Test failed: ${err.error?.error || 'no response'}`, 'Close', { duration: 6000 });
        this.cdr.markForCheck();
      },
    });
  }

  createKey(): void {
    const label = this.newKeyLabel.trim();
    if (!label) {
      this.snackBar.open('Give the key a name, such as the host or monitor that uses it', 'Close', { duration: 3500 });
      return;
    }
    this.http.post<{ plaintext: string }>('/api/admin/server-alerts/keys', { label }, { headers: this.adminService.getAuthHeaders() }).subscribe({
      next: (res) => {
        this.newKeyLabel = '';
        this.newKeyPlaintext = res.plaintext;
        this.load();
      },
      error: (err) => this.snackBar.open(err.error?.error || 'Could not create the key', 'Close', { duration: 4000 }),
    });
  }

  copyKey(): void {
    navigator.clipboard?.writeText(this.newKeyPlaintext).then(
      () => this.snackBar.open('Key copied', 'Close', { duration: 2500 }),
      () => this.snackBar.open('Select the key and copy it', 'Close', { duration: 4000 }),
    );
  }

  dismissKey(): void {
    this.newKeyPlaintext = '';
    this.cdr.markForCheck();
  }

  toggleKey(k: AlertKey): void {
    this.http.put(`/api/admin/server-alerts/keys/${k.id}`, { enabled: !k.enabled }, { headers: this.adminService.getAuthHeaders() }).subscribe({
      next: () => this.load(),
      error: (err) => this.snackBar.open(err.error?.error || 'Could not update the key', 'Close', { duration: 4000 }),
    });
  }

  removeKey(k: AlertKey): void {
    if (!confirm(`Delete "${k.label}"? Monitors using it can no longer post alerts.`)) {
      return;
    }
    this.http.delete(`/api/admin/server-alerts/keys/${k.id}`, { headers: this.adminService.getAuthHeaders() }).subscribe({
      next: () => this.load(),
      error: (err) => this.snackBar.open(err.error?.error || 'Could not delete the key', 'Close', { duration: 4000 }),
    });
  }
}
