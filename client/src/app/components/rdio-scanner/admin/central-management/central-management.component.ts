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

import { ChangeDetectionStrategy, ChangeDetectorRef, Component, EventEmitter, Input, Output } from '@angular/core';
import { FormGroup } from '@angular/forms';
import { HttpClient, HttpHeaders } from '@angular/common/http';
import { MatSnackBar } from '@angular/material/snack-bar';

@Component({
  selector: 'rdio-scanner-admin-central-management',
  templateUrl: './central-management.component.html',
  styleUrls: ['./central-management.component.scss'],
  changeDetection: ChangeDetectionStrategy.Eager,
  standalone: false,
})
export class RdioScannerAdminCentralManagementComponent {
  @Input() options!: FormGroup;
  /** Emitted after a successful leave so the parent can leave this section. */
  @Output() leftCentralManagement = new EventEmitter<void>();

  leaveCMCode = '';
  leaveCMError = '';
  leavingCM = false;

  constructor(
    private http: HttpClient,
    private cdr: ChangeDetectorRef,
    private snackBar: MatSnackBar,
  ) {}

  get isCentrallyManaged(): boolean {
    return this.options?.get('centralManagementEnabled')?.value === true;
  }

  get portalUrl(): string {
    return (this.options?.get('centralManagementURL')?.value || '').trim();
  }

  get serverName(): string {
    return (this.options?.get('centralManagementServerName')?.value || '').trim();
  }

  get serverId(): string {
    return (this.options?.get('centralManagementServerID')?.value || '').trim();
  }

  leaveCentralManagement(): void {
    if (!this.leaveCMCode.trim() || this.leavingCM) {
      return;
    }
    this.leavingCM = true;
    this.leaveCMError = '';

    const token = sessionStorage.getItem('rdio-scanner-admin-token');
    const headers = new HttpHeaders({ Authorization: token || '' });

    this.http.post('/api/central-management/leave', {
      code: this.leaveCMCode.toUpperCase().trim(),
    }, { headers }).subscribe({
      next: (res: any) => {
        this.leavingCM = false;
        this.leaveCMCode = '';
        this.options?.get('centralManagementEnabled')?.setValue(false);
        this.options?.get('centralManagementURL')?.setValue('');
        this.options?.get('centralManagementAPIKey')?.setValue('');
        this.options?.get('centralManagementServerName')?.setValue('');
        this.options?.get('centralManagementServerID')?.setValue('');
        this.snackBar.open(res?.message || 'Server removed from Central Management.', 'Close', {
          duration: 5000,
          panelClass: ['success-snackbar'],
        });
        this.leftCentralManagement.emit();
        this.cdr.markForCheck();
      },
      error: (err) => {
        this.leavingCM = false;
        if (typeof err?.error === 'string' && err.error.trim()) {
          this.leaveCMError = err.error.trim();
        } else {
          this.leaveCMError = err?.error?.error || err?.error?.message
            || 'Failed to leave Central Management. Check the code and try again.';
        }
        this.cdr.markForCheck();
      },
    });
  }
}
