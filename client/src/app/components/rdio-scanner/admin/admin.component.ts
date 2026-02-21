/*
 * *****************************************************************************
 * Copyright (C) 2019-2024 Chrystian Huot <chrystian@huot.qc.ca>
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

import { Component, OnDestroy, ViewChild, ViewEncapsulation } from '@angular/core';
import { MatTabChangeEvent } from '@angular/material/tabs';
import { Title } from '@angular/platform-browser';
import { AdminEvent, RdioScannerAdminService } from './admin.service';
import { RdioScannerAdminLogsComponent } from './logs/logs.component';

@Component({
    encapsulation: ViewEncapsulation.None,
    selector: 'rdio-scanner-admin',
    styleUrls: ['./admin.component.scss'],
    templateUrl: './admin.component.html',
})
export class RdioScannerAdminComponent implements OnDestroy {
    authenticated = false;

    /** Controls which top-level tab is active (0=Config, 1=Logs, 2=System Health, 3=Tools) */
    selectedTabIndex = 0;

    @ViewChild('logsComponent') private logsComponent: RdioScannerAdminLogsComponent | undefined;

    private eventSubscription;

    constructor(
        private adminService: RdioScannerAdminService,
        private titleService: Title,
    ) {
        // Initialize authenticated state from admin service
        this.authenticated = this.adminService.authenticated;

        // Set initial title if already authenticated
        if (this.authenticated) {
            this.updateTitle();
        }

        this.eventSubscription = this.adminService.event.subscribe(async (event: AdminEvent) => {
            if ('authenticated' in event) {
                this.authenticated = event.authenticated || false;

                if (this.authenticated) {
                    this.updateTitle();
                }
            }

            if ('config' in event && event.config) {
                const branding = event.config.branding?.trim() || 'TLR';
                this.titleService.setTitle(`Admin-${branding}`);
            }
        });
    }

    /** Called when the top-level tab changes — auto-reloads Logs when selected. */
    onTabChange(event: MatTabChangeEvent): void {
        if (event.index === 1 && this.logsComponent) {
            this.logsComponent.reload();
        }
    }

    private async updateTitle(): Promise<void> {
        try {
            const config = await this.adminService.getConfig();
            const branding = config.branding?.trim() || 'TLR';
            this.titleService.setTitle(`Admin-${branding}`);
        } catch {
            this.titleService.setTitle('Admin-TLR');
        }
    }

    ngOnDestroy(): void {
        this.eventSubscription.unsubscribe();
    }

    async logout(): Promise<void> {
        await this.adminService.logout();
    }
}
