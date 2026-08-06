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

import { ChangeDetectorRef, Component, OnDestroy, OnInit } from '@angular/core';
import {
    ListenerUserSnapshot,
    ListenersSnapshot,
    RdioScannerAdminService,
} from '../admin.service';

@Component({
    selector: 'rdio-scanner-admin-listeners',
    templateUrl: './listeners.component.html',
    styleUrls: ['./listeners.component.scss'],
    standalone: false,
})
export class RdioScannerAdminListenersComponent implements OnInit, OnDestroy {
    loading = false;
    error: string | null = null;
    snapshot: ListenersSnapshot | null = null;
    expandedUserIds = new Set<number | string>();
    private pollTimer: ReturnType<typeof setInterval> | null = null;
    private destroyed = false;

    constructor(
        private adminService: RdioScannerAdminService,
        private cdr: ChangeDetectorRef,
    ) {}

    ngOnInit(): void {
        void this.refresh(true);
        this.pollTimer = setInterval(() => {
            void this.refresh(false);
        }, 5000);
    }

    ngOnDestroy(): void {
        this.destroyed = true;
        if (this.pollTimer) {
            clearInterval(this.pollTimer);
            this.pollTimer = null;
        }
    }

    async refresh(showSpinner: boolean = true): Promise<void> {
        if (showSpinner) {
            this.loading = true;
            this.error = null;
        }
        try {
            const snapshot = await this.adminService.getListeners();
            if (this.destroyed) {
                return;
            }
            this.snapshot = snapshot;
            this.error = null;
        } catch (err: any) {
            if (this.destroyed) {
                return;
            }
            this.error = err?.message || 'Failed to load listeners';
        } finally {
            if (!this.destroyed) {
                this.loading = false;
                this.cdr.detectChanges();
            }
        }
    }

    trackUser(_: number, user: ListenerUserSnapshot): string | number {
        return user.anonymous ? 'anonymous' : user.userId;
    }

    displayName(user: ListenerUserSnapshot): string {
        if (user.anonymous) {
            return 'Anonymous';
        }
        const name = `${user.firstName || ''} ${user.lastName || ''}`.trim();
        return name || user.email || `User #${user.userId}`;
    }

    expansionKey(user: ListenerUserSnapshot): string | number {
        return user.anonymous ? 'anonymous' : user.userId;
    }

    isExpanded(user: ListenerUserSnapshot): boolean {
        return this.expandedUserIds.has(this.expansionKey(user));
    }

    toggleExpanded(user: ListenerUserSnapshot): void {
        const key = this.expansionKey(user);
        if (this.expandedUserIds.has(key)) {
            this.expandedUserIds.delete(key);
        } else {
            this.expandedUserIds.add(key);
        }
    }

    kindLabel(kind: string): string {
        return kind === 'mobile' ? 'Mobile' : 'Web';
    }

    connectedDuration(iso: string): string {
        if (!iso) {
            return '—';
        }
        const connectedAt = new Date(iso).getTime();
        if (Number.isNaN(connectedAt)) {
            return '—';
        }
        const seconds = Math.max(0, Math.floor((Date.now() - connectedAt) / 1000));
        if (seconds < 60) {
            return `${seconds}s`;
        }
        const minutes = Math.floor(seconds / 60);
        if (minutes < 60) {
            return `${minutes}m`;
        }
        const hours = Math.floor(minutes / 60);
        const remMin = minutes % 60;
        if (hours < 48) {
            return remMin ? `${hours}h ${remMin}m` : `${hours}h`;
        }
        const days = Math.floor(hours / 24);
        return `${days}d`;
    }
}
