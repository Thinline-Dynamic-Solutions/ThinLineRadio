/*
 * *****************************************************************************
 * Copyright (C) 2026 Thinline Dynamic Solutions
 *
 * Licensed under the GNU GPL v3 (or later). See LICENSE.
 * ****************************************************************************
 */

import { ChangeDetectionStrategy, ChangeDetectorRef, Component, OnDestroy, OnInit } from '@angular/core';
import { HttpClient } from '@angular/common/http';
import { MatSnackBar } from '@angular/material/snack-bar';
import { RdioScannerAdminService } from '../../admin.service';

interface DispatchUser {
  id: number;
  name: string;
  email: string;
  groupIds: number[];
  devices: number;
}

interface DispatchGroup {
  id: number;
  name: string;
  members: number;
}

interface DispatchHistoryItem {
  id: number;
  title: string;
  message: string;
  label: string;
  severity: string;
  createdAt: number;
  sentBy: string;
  recipientCount: number;
  deviceCount: number;
  groups: string[];
}

@Component({
  selector: 'rdio-scanner-admin-dispatch-alerts',
  templateUrl: './dispatch-alerts.component.html',
  styleUrls: ['./dispatch-alerts.component.scss'],
  changeDetection: ChangeDetectionStrategy.OnPush,
  standalone: false,
})
export class RdioScannerAdminDispatchAlertsComponent implements OnInit, OnDestroy {
  readonly severities = ['critical', 'error', 'warning', 'info'];

  users: DispatchUser[] = [];
  groups: DispatchGroup[] = [];
  history: DispatchHistoryItem[] = [];
  sounds: string[] = [];
  sound = '';
  savedSound = '';
  canEditSound = false;

  label = 'Dispatch Alert';
  title = '';
  message = '';
  severity = 'critical';
  userFilter = '';
  selectedUsers = new Set<number>();
  selectedGroups = new Set<number>();
  sending = false;
  loading = false;

  private preview?: HTMLAudioElement;

  constructor(
    private http: HttpClient,
    private adminService: RdioScannerAdminService,
    private snackBar: MatSnackBar,
    private cdr: ChangeDetectorRef,
  ) {}

  ngOnInit(): void {
    this.load();
  }

  ngOnDestroy(): void {
    this.preview?.pause();
  }

  private get headers() {
    return this.adminService.getAuthHeaders();
  }

  load(): void {
    this.loading = true;
    this.http.get<{ users: DispatchUser[]; groups: DispatchGroup[] }>('/api/admin/dispatch-alerts/recipients', { headers: this.headers }).subscribe({
      next: (res) => {
        this.users = (res.users || []).sort((a, b) => a.name.localeCompare(b.name));
        this.groups = (res.groups || []).sort((a, b) => a.name.localeCompare(b.name));
        this.loading = false;
        this.cdr.markForCheck();
      },
      error: (err) => {
        this.loading = false;
        this.snackBar.open(err.error?.error || 'Could not load users and groups', 'Close', { duration: 4000 });
        this.cdr.markForCheck();
      },
    });
    this.loadHistory();
  }

  loadHistory(): void {
    this.http.get<{ history: DispatchHistoryItem[]; sound: string; sounds: string[]; canEditSound: boolean }>('/api/admin/dispatch-alerts', { headers: this.headers }).subscribe({
      next: (res) => {
        this.history = res.history || [];
        this.sounds = res.sounds || [];
        this.sound = this.savedSound = res.sound;
        this.canEditSound = res.canEditSound;
        this.cdr.markForCheck();
      },
      error: (err) => this.snackBar.open(err.error?.error || 'Could not load dispatch history', 'Close', { duration: 4000 }),
    });
  }

  get filteredUsers(): DispatchUser[] {
    const q = this.userFilter.trim().toLowerCase();
    const list = q
      ? this.users.filter(u => u.name.toLowerCase().includes(q) || u.email.toLowerCase().includes(q))
      : this.users;
    return list.slice(0, 100);
  }

  get selectedUserList(): DispatchUser[] {
    return this.users.filter(u => this.selectedUsers.has(u.id));
  }

  /** Everyone the alert would reach: picked users plus members of picked groups. */
  get reach(): { people: number; devices: number; noDevice: number } {
    let people = 0, devices = 0, noDevice = 0;
    for (const u of this.users) {
      if (this.selectedUsers.has(u.id) || u.groupIds.some(g => this.selectedGroups.has(g))) {
        people++;
        devices += u.devices;
        if (!u.devices) {
          noDevice++;
        }
      }
    }
    return { people, devices, noDevice };
  }

  toggleGroup(g: DispatchGroup): void {
    this.selectedGroups.has(g.id) ? this.selectedGroups.delete(g.id) : this.selectedGroups.add(g.id);
  }

  toggleUser(u: DispatchUser): void {
    this.selectedUsers.has(u.id) ? this.selectedUsers.delete(u.id) : this.selectedUsers.add(u.id);
  }

  soundName(file: string): string {
    return file.replace(/\.wav$/, '').replace(/_/g, ' ');
  }

  playSound(file = this.sound): void {
    this.preview?.pause();
    this.preview = new Audio(`/assets/sounds/${file}`);
    this.preview.play().catch(() => undefined);
  }

  saveSound(): void {
    this.http.put<{ sound: string }>('/api/admin/dispatch-alerts/settings', { sound: this.sound }, { headers: this.headers }).subscribe({
      next: (res) => {
        this.savedSound = res.sound;
        this.snackBar.open(`Dispatch tone set to ${this.soundName(res.sound)}`, 'Close', { duration: 3000 });
        this.cdr.markForCheck();
      },
      error: (err) => this.snackBar.open(err.error?.error || 'Could not save the tone', 'Close', { duration: 4000 }),
    });
  }

  send(): void {
    const reach = this.reach;
    if (!this.message.trim()) {
      this.snackBar.open('Write a message', 'Close', { duration: 3000 });
      return;
    }
    if (!reach.people) {
      this.snackBar.open('Pick at least one user or group', 'Close', { duration: 3000 });
      return;
    }
    if (!confirm(`Send "${this.title || this.label}" to ${reach.people} ${reach.people === 1 ? 'person' : 'people'}? It plays the dispatch tone on their phones whatever their alert settings are.`)) {
      return;
    }
    this.sending = true;
    const body = {
      label: this.label.trim(),
      title: this.title.trim(),
      message: this.message.trim(),
      severity: this.severity,
      userIds: [...this.selectedUsers],
      groupIds: [...this.selectedGroups],
    };
    this.http.post<{ recipients: number; devices: number }>('/api/admin/dispatch-alerts', body, { headers: this.headers }).subscribe({
      next: (res) => {
        this.sending = false;
        this.title = '';
        this.message = '';
        this.snackBar.open(`Sent to ${res.recipients} ${res.recipients === 1 ? 'person' : 'people'} on ${res.devices} ${res.devices === 1 ? 'device' : 'devices'}`, 'Close', { duration: 4000 });
        this.loadHistory();
        this.cdr.markForCheck();
      },
      error: (err) => {
        this.sending = false;
        this.snackBar.open(err.error?.error || 'Could not send the alert', 'Close', { duration: 5000 });
        this.cdr.markForCheck();
      },
    });
  }
}
