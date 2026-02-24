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

import { Component, OnInit, OnDestroy } from '@angular/core';
import { Title } from '@angular/platform-browser';
import { Subscription } from 'rxjs';
import packageInfo from '../../../../../package.json';
import { RdioScannerAdminService, AdminEvent } from '../../../components/rdio-scanner/admin/admin.service';

@Component({
    selector: 'rdio-scanner-admin-page',
    styleUrls: ['./admin.component.scss'],
    templateUrl: './admin.component.html',
})
export class RdioScannerAdminPageComponent implements OnInit, OnDestroy {
    version = packageInfo.version;
    authenticated = false;

    private eventSubscription: Subscription | undefined;

    constructor(
        private adminService: RdioScannerAdminService,
        private titleService: Title,
    ) {
        // Auto-login when Central Management opens this page with a cm_token query param.
        // This must run before reading `authenticated` so the token is in sessionStorage first.
        const params = new URLSearchParams(window.location.search);
        const cmToken = params.get('cm_token');
        if (cmToken) {
            this.adminService.setTokenFromExternal(cmToken);
            // Clean the token from the address bar so it isn't bookmarked or leaked
            window.history.replaceState({}, '', window.location.pathname);
        }

        this.authenticated = this.adminService.authenticated;
    }

    ngOnInit(): void {
        this.titleService.setTitle('Admin-TLR');

        if (this.adminService.authenticated) {
            this.updateTitle();
        }

        this.eventSubscription = this.adminService.event.subscribe(async (event: AdminEvent) => {
            if ('authenticated' in event) {
                this.authenticated = event.authenticated ?? false;
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

    ngOnDestroy(): void {
        this.eventSubscription?.unsubscribe();
    }

    async logout(): Promise<void> {
        await this.adminService.logout();
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
}
