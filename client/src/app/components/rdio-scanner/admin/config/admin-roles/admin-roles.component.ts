/*
 * *****************************************************************************
 * Copyright (C) 2026 Thinline Dynamic Solutions
 *
 * Licensed under the GNU GPL v3 (or later). See LICENSE.
 * ****************************************************************************
 */

import { ChangeDetectionStrategy, ChangeDetectorRef, Component, OnInit } from '@angular/core';
import { MatSnackBar } from '@angular/material/snack-bar';
import { AdminPermission, AdminRole, RdioScannerAdminService } from '../../admin.service';

interface PermissionGroup {
  name: string;
  permissions: AdminPermission[];
}

const ROLE_PRESETS: { name: string; permissions: string[] }[] = [
  {
    name: 'Admin Moderator',
    permissions: [
      'users.view', 'users.edit', 'users.suspend', 'users.password', 'users.delayModeration',
      'listeners.view', 'shareLinks.manage', 'logs.view', 'health.view', 'dispatch.send',
      'content.transcripts', 'content.incidentPins', 'content.callNatures',
    ],
  },
  {
    name: 'Support',
    permissions: ['users.view', 'users.password', 'listeners.view', 'logs.view'],
  },
];

@Component({
  selector: 'rdio-scanner-admin-roles',
  templateUrl: './admin-roles.component.html',
  styleUrls: ['./admin-roles.component.scss'],
  changeDetection: ChangeDetectionStrategy.OnPush,
  standalone: false,
})
export class RdioScannerAdminRolesComponent implements OnInit {
  roles: AdminRole[] = [];
  groups: PermissionGroup[] = [];
  presets = ROLE_PRESETS;
  loading = false;
  saving = false;

  editing: AdminRole | null = null;
  showForm = false;
  name = '';
  selected = new Set<string>();

  private labels = new Map<string, string>();

  constructor(
    private adminService: RdioScannerAdminService,
    private snackBar: MatSnackBar,
    private cdr: ChangeDetectorRef,
  ) {}

  ngOnInit(): void {
    this.load();
  }

  async load(): Promise<void> {
    this.loading = true;
    this.cdr.markForCheck();
    try {
      const res = await this.adminService.getAdminRoles();
      this.roles = res.roles || [];
      const groups = new Map<string, AdminPermission[]>();
      for (const p of res.permissions || []) {
        this.labels.set(p.key, p.label);
        groups.set(p.group, [...(groups.get(p.group) || []), p]);
      }
      this.groups = [...groups].map(([name, permissions]) => ({ name, permissions }));
    } catch (error: any) {
      this.fail(error, 'Failed to load admin roles');
    } finally {
      this.loading = false;
      this.cdr.markForCheck();
    }
  }

  startCreate(): void {
    this.editing = null;
    this.name = '';
    this.selected = new Set();
    this.showForm = true;
    this.cdr.markForCheck();
  }

  startEdit(role: AdminRole): void {
    this.editing = role;
    this.name = role.name;
    this.selected = new Set(role.permissions);
    this.showForm = true;
    this.cdr.markForCheck();
  }

  applyPreset(preset: { name: string; permissions: string[] }): void {
    if (!this.name.trim()) {
      this.name = preset.name;
    }
    this.selected = new Set(preset.permissions);
    this.cdr.markForCheck();
  }

  cancel(): void {
    this.showForm = false;
    this.editing = null;
    this.cdr.markForCheck();
  }

  toggle(key: string, checked: boolean): void {
    if (checked) {
      this.selected.add(key);
    } else {
      this.selected.delete(key);
    }
  }

  groupChecked(group: PermissionGroup): boolean {
    return group.permissions.every(p => this.selected.has(p.key));
  }

  groupPartial(group: PermissionGroup): boolean {
    return !this.groupChecked(group) && group.permissions.some(p => this.selected.has(p.key));
  }

  toggleGroup(group: PermissionGroup, checked: boolean): void {
    group.permissions.forEach(p => this.toggle(p.key, checked));
    this.cdr.markForCheck();
  }

  async save(): Promise<void> {
    if (!this.name.trim() || this.saving) {
      return;
    }
    this.saving = true;
    this.cdr.markForCheck();
    try {
      await this.adminService.saveAdminRole({
        id: this.editing?.id,
        name: this.name.trim(),
        permissions: [...this.selected],
      });
      this.snackBar.open(this.editing ? 'Role updated' : 'Role created', '', { duration: 3000 });
      this.showForm = false;
      this.editing = null;
      await this.load();
    } catch (error: any) {
      this.fail(error, 'Failed to save role');
    } finally {
      this.saving = false;
      this.cdr.markForCheck();
    }
  }

  async remove(role: AdminRole): Promise<void> {
    const members = role.members ? ` ${role.members} user(s) will lose admin access.` : '';
    if (!confirm(`Delete the "${role.name}" role?${members}`)) {
      return;
    }
    try {
      await this.adminService.deleteAdminRole(role.id);
      await this.load();
    } catch (error: any) {
      this.fail(error, 'Failed to delete role');
    }
  }

  summary(role: AdminRole): string {
    if (!role.permissions.length) {
      return 'No permissions';
    }
    return role.permissions.map(p => this.labels.get(p) || p).join(', ');
  }

  private fail(error: any, fallback: string): void {
    this.snackBar.open(error?.error?.error || fallback, 'Close', { duration: 5000, panelClass: ['error-snackbar'] });
  }
}
