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

import { Component, OnInit, OnDestroy, ViewChild } from '@angular/core';
import { Title } from '@angular/platform-browser';
import { Subscription } from 'rxjs';
import { RdioScannerAdminService, AdminEvent } from '../../../components/rdio-scanner/admin/admin.service';
import { RdioScannerAdminComponent } from '../../../components/rdio-scanner/admin/admin.component';

@Component({
    selector: 'rdio-scanner-admin-page',
    styleUrls: ['./admin.component.scss'],
    templateUrl: './admin.component.html',
})
export class RdioScannerAdminPageComponent implements OnInit, OnDestroy {
    version = '';
    authenticated = false;

    @ViewChild('adminComponent') private adminComponent: RdioScannerAdminComponent | undefined;

    get formDirty(): boolean { return !!(this.adminComponent?.formDirty); }
    get formValid(): boolean { return !!(this.adminComponent?.formValid); }
    get configLoading(): boolean { return !!(this.adminComponent?.configLoading); }
    get onConfigTab(): boolean { return this.adminComponent?.selectedTabIndex === 0; }

    saveConfig(): void { this.adminComponent?.saveConfig(); }
    resetConfig(): void { window.location.reload(); }

    private eventSubscription: Subscription | undefined;

    constructor(
        private adminService: RdioScannerAdminService,
        private titleService: Title,
    ) {
        // Auto-login via cm_token (Central Management one-click) or sso_token (system admin SSO).
        // Must run before reading `authenticated` so the token is in sessionStorage first.
        const params = new URLSearchParams(window.location.search);
        const cmToken = params.get('cm_token');
        const ssoToken = params.get('sso_token');
        if (cmToken) {
            this.adminService.setTokenFromExternal(cmToken);
            window.history.replaceState({}, '', window.location.pathname);
        } else if (ssoToken) {
            this.adminService.setTokenFromExternal(ssoToken);
            window.history.replaceState({}, '', window.location.pathname);
        }

        this.authenticated = this.adminService.authenticated;
    }

    ngOnInit(): void {
        this.titleService.setTitle('Admin-TLR');

        // Fetch the real server version immediately — no auth required.
        this.adminService.getLoginConfig().then(cfg => {
            if (cfg.version) {
                this.version = cfg.version;
            }
        });

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
                if (event.config.version) {
                    this.version = event.config.version;
                }
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
            if (config.version) {
                this.version = config.version;
            }
        } catch {
            this.titleService.setTitle('Admin-TLR');
        }
    }
}
