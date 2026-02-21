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

import { Component, EventEmitter, Output, ViewEncapsulation } from '@angular/core';
import { Config } from '../admin.service';

export interface ToolSection {
    id: string;
    label: string;
    icon: string;
    description: string;
}

@Component({
    encapsulation: ViewEncapsulation.None,
    selector: 'rdio-scanner-admin-tools',
    templateUrl: './tools.component.html',
    styleUrls: ['./tools.component.scss'],
})
export class RdioScannerAdminToolsComponent {
    @Output() config = new EventEmitter<Config>();

    activeSection = 'import-talkgroups';

    readonly toolSections: ToolSection[] = [
        { id: 'import-talkgroups',    label: 'Import Talkgroups',     icon: 'description',    description: 'Import talkgroup definitions from a CSV or JSON file' },
        { id: 'import-units',         label: 'Import Units',          icon: 'description',    description: 'Import unit definitions from a CSV or JSON file' },
        { id: 'radio-reference',      label: 'Radio Reference',       icon: 'cloud_download', description: 'Import system data directly from RadioReference.com' },
        { id: 'admin-password',       label: 'Admin Password',        icon: 'password',       description: 'Change the admin panel password' },
        { id: 'import-export-config', label: 'Import/Export Config',  icon: 'sync_alt',       description: 'Backup or restore the full configuration' },
        { id: 'config-sync',          label: 'Config Sync',           icon: 'cloud_sync',     description: 'Synchronize configuration with a remote server' },
        { id: 'stripe-sync',          label: 'Stripe Customer Sync',  icon: 'payment',        description: 'Sync subscriber access with Stripe customers' },
        { id: 'purge-data',           label: 'Purge Data',            icon: 'delete_forever', description: 'Permanently delete stored audio and call records' },
    ];

    get activeToolSection(): ToolSection | undefined {
        return this.toolSections.find(t => t.id === this.activeSection);
    }

    setSection(id: string): void {
        this.activeSection = id;
    }
}
