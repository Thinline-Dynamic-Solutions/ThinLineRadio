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
import { MatDialog } from '@angular/material/dialog';
import { MatSnackBar } from '@angular/material/snack-bar';
import { RdioScannerAdminService, System } from '../../admin.service';
import { RdioScannerAdminSystemsSelectComponent, SYSTEMS_SELECT_DIALOG_OPTIONS } from '../systems/select/select.component';

interface LinkScope {
  id: number;
  talkgroups: '*' | number[];
}

interface ShareLinkFeatures {
  liveFeed: boolean;
  replay: boolean;
  channels: boolean;
  settings: boolean;
}

interface ShareLink {
  id: number;
  token: string;
  name: string;
  systemAccess: string;
  delay: number;
  features: ShareLinkFeatures & Record<string, boolean>;
  enabled: boolean;
  expiresAt: number;
  createdAt: number;
  active: boolean;
  listeners: number;
}

@Component({
  selector: 'rdio-scanner-admin-share-links',
  templateUrl: './share-links.component.html',
  styleUrls: ['./share-links.component.scss'],
  changeDetection: ChangeDetectionStrategy.OnPush,
  standalone: false,
})
export class RdioScannerAdminShareLinksComponent implements OnInit {
  links: ShareLink[] = [];
  loading = false;
  saving = false;
  editing: ShareLink | null = null;
  showForm = false;
  scope: LinkScope[] = [];
  form: FormGroup;

  private systems: System[] = [];
  private configGroups: any[] = [];
  private configTags: any[] = [];

  constructor(
    private http: HttpClient,
    private fb: FormBuilder,
    private adminService: RdioScannerAdminService,
    private matDialog: MatDialog,
    private snackBar: MatSnackBar,
    private cdr: ChangeDetectorRef,
  ) {
    this.form = this.fb.group({
      name: ['', Validators.required],
      delay: [30, [Validators.min(0)]],
      expiresOn: [''],
      enabled: [true],
      features: this.fb.group({
        liveFeed: [true],
        replay: [false],
        channels: [false],
        settings: [false],
      }),
    });
  }

  ngOnInit(): void {
    this.load();
    this.adminService.getConfig().then(config => {
      this.systems = config.systems || [];
      this.configGroups = config.groups || [];
      this.configTags = config.tags || [];
      this.cdr.markForCheck();
    });
  }

  linkUrl(link: ShareLink): string {
    return `${window.location.origin}/l/${link.token}`;
  }

  load(): void {
    this.loading = true;
    this.http.get<{ links: ShareLink[] }>('/api/admin/listen-links', { headers: this.adminService.getAuthHeaders() }).subscribe({
      next: (res) => {
        this.links = res.links || [];
        this.loading = false;
        this.cdr.markForCheck();
      },
      error: (err) => {
        this.loading = false;
        this.snackBar.open(err.error?.error || 'Could not load share links', 'Close', { duration: 4000 });
        this.cdr.markForCheck();
      },
    });
  }

  startCreate(): void {
    this.editing = null;
    this.scope = [];
    this.form.reset({
      name: '',
      delay: 30,
      expiresOn: '',
      enabled: true,
      features: { liveFeed: true, replay: false, channels: false, settings: false },
    });
    this.showForm = true;
    this.cdr.markForCheck();
  }

  startEdit(link: ShareLink): void {
    this.editing = link;
    this.scope = this.parseScope(link.systemAccess);
    this.form.reset({
      name: link.name,
      delay: link.delay,
      expiresOn: link.expiresAt ? new Date(link.expiresAt * 1000).toISOString().slice(0, 10) : '',
      enabled: link.enabled,
      features: {
        liveFeed: link.features?.liveFeed !== false,
        replay: !!link.features?.replay,
        channels: !!link.features?.channels,
        settings: !!link.features?.settings,
      },
    });
    this.showForm = true;
    this.cdr.markForCheck();
  }

  cancel(): void {
    this.showForm = false;
    this.editing = null;
    this.cdr.markForCheck();
  }

  get scopeSummary(): string {
    return this.summarize(this.scope);
  }

  linkScopeSummary(link: ShareLink): string {
    return this.summarize(this.parseScope(link.systemAccess));
  }

  pickChannels(): void {
    const rootForm = this.fb.group({
      groups: this.fb.array(this.configGroups.map((g: any) => this.adminService.newGroupForm(g))),
      tags: this.fb.array(this.configTags.map((t: any) => this.adminService.newTagForm(t))),
      systems: this.fb.array(this.systems.map((s: any) => this.adminService.newSystemForm(s, true, true))),
      access: this.fb.group({ systems: [this.scope] }),
    });
    const ref = this.matDialog.open(RdioScannerAdminSystemsSelectComponent, {
      ...SYSTEMS_SELECT_DIALOG_OPTIONS,
      data: { access: rootForm.get('access') as FormGroup, rawSystems: this.systems },
    });
    ref.afterClosed().subscribe((data) => {
      if (data === null || data === undefined) {
        return;
      }
      if (data === '*') {
        this.scope = this.systems.map((s: any) => ({ id: s.systemRef ?? s.id, talkgroups: '*' as const }));
      } else if (Array.isArray(data)) {
        this.scope = data;
      }
      this.cdr.markForCheck();
    });
  }

  save(): void {
    if (this.form.invalid) {
      this.form.markAllAsTouched();
      return;
    }
    if (!this.scope.length) {
      this.snackBar.open('Pick at least one channel for this link', 'Close', { duration: 3000 });
      return;
    }
    const v = this.form.value;
    const expiresAt = v.expiresOn ? Math.floor(new Date(`${v.expiresOn}T23:59:59`).getTime() / 1000) : 0;
    const body = {
      name: (v.name || '').trim(),
      systemAccess: JSON.stringify(this.scope),
      delay: Math.max(0, Number(v.delay) || 0),
      expiresAt,
      enabled: !!v.enabled,
      features: {
        liveFeed: !!v.features.liveFeed,
        replay: !!v.features.replay,
        channels: !!v.features.channels,
        settings: !!v.features.settings,
        alerts: false, transcripts: false, stats: false, map: false, alertsSideRail: false,
      },
    };
    const headers = this.adminService.getAuthHeaders();
    const req = this.editing
      ? this.http.put<ShareLink>(`/api/admin/listen-links/${this.editing.id}`, body, { headers })
      : this.http.post<ShareLink>('/api/admin/listen-links', body, { headers });
    this.saving = true;
    req.subscribe({
      next: (link) => {
        this.saving = false;
        this.showForm = false;
        const created = !this.editing;
        this.editing = null;
        this.load();
        if (created) {
          this.copy(link);
        }
        this.cdr.markForCheck();
      },
      error: (err) => {
        this.saving = false;
        this.snackBar.open(err.error?.error || 'Could not save the link', 'Close', { duration: 4000 });
        this.cdr.markForCheck();
      },
    });
  }

  toggleEnabled(link: ShareLink): void {
    const body = {
      name: link.name,
      systemAccess: link.systemAccess,
      delay: link.delay,
      expiresAt: link.expiresAt,
      enabled: !link.enabled,
    };
    this.http.put(`/api/admin/listen-links/${link.id}`, body, { headers: this.adminService.getAuthHeaders() }).subscribe({
      next: () => this.load(),
      error: (err) => this.snackBar.open(err.error?.error || 'Could not update the link', 'Close', { duration: 4000 }),
    });
  }

  remove(link: ShareLink): void {
    if (!confirm(`Delete "${link.name}"? Anyone listening on it is disconnected and the link stops working.`)) {
      return;
    }
    this.http.delete(`/api/admin/listen-links/${link.id}`, { headers: this.adminService.getAuthHeaders() }).subscribe({
      next: () => this.load(),
      error: (err) => this.snackBar.open(err.error?.error || 'Could not delete the link', 'Close', { duration: 4000 }),
    });
  }

  copy(link: ShareLink): void {
    const url = this.linkUrl(link);
    navigator.clipboard?.writeText(url).then(
      () => this.snackBar.open('Link copied', 'Close', { duration: 2500 }),
      () => this.snackBar.open(url, 'Close', { duration: 8000 }),
    );
  }

  statusLabel(link: ShareLink): string {
    if (!link.enabled) {
      return 'Off';
    }
    return link.active ? 'Live' : 'Expired';
  }

  private parseScope(raw: string): LinkScope[] {
    try {
      const parsed = JSON.parse(raw || '[]');
      if (!Array.isArray(parsed)) {
        return [];
      }
      return parsed.map((s: any) => (typeof s === 'object' && s !== null ? s : { id: s, talkgroups: '*' }));
    } catch {
      return [];
    }
  }

  private summarize(scope: LinkScope[]): string {
    if (!scope.length) {
      return 'No channels picked';
    }
    const labelFor = (id: number) => {
      const sys: any = this.systems.find((s: any) => (s.systemRef ?? s.id) === id || s.id === id);
      return sys?.label || `System ${id}`;
    };
    return scope
      .map(s => s.talkgroups === '*'
        ? `${labelFor(s.id)} (all)`
        : `${labelFor(s.id)} (${s.talkgroups.length} talkgroup${s.talkgroups.length === 1 ? '' : 's'})`)
      .join(', ');
  }
}
