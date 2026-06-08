/*
 * *****************************************************************************
 * Copyright (C) 2019-2024 Chrystian Huot <chrystian@huot.qc.ca>
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

import { Component, EventEmitter, OnInit, Output } from '@angular/core';
import { Config, RdioScannerAdminService, System } from '../../admin.service';

@Component({
    selector: 'rdio-scanner-admin-import-units',
    styleUrls: ['./import-units.component.scss'],
    templateUrl: './import-units.component.html',
})
export class RdioScannerAdminImportUnitsComponent implements OnInit{
    @Output() config = new EventEmitter<Config>();

    baseConfig: Config = {};

    csv: string[][] = [];

    system: System | undefined;

    tableColumns = ['id', 'label', 'action'];

    constructor(private adminService: RdioScannerAdminService) { }

    async ngOnInit(): Promise<void> {
        // Only load data if user is authenticated
        if (this.adminService.authenticated) {
            this.baseConfig = await this.adminService.getConfig();

            if (Array.isArray(this.baseConfig.systems) && this.baseConfig.systems.length > 0) {
                this.system = this.baseConfig.systems[0];
            }
        }
    }

    async import(): Promise<void> {
        if (this.system === undefined) return;

        const units = this.csv.map((csv, idx) => ({
            unitRef: +csv[0],
            label: csv[1],
            order: idx + 1,
        }));

        this.system.units = (this.system.units || []).concat(units);

        this.csv = [];

        // Persist directly — the global Save bar was removed. Bulk imports are a
        // whole-config write (the loaded config holds every system/unit). Strip the
        // user-scoped collections so we never clobber them; they have their own endpoints.
        const payload: any = { ...this.baseConfig };
        delete payload.users;
        delete payload.userGroups;
        delete payload.keywordLists;
        delete payload.userAlertPreferences;
        delete payload.deviceTokens;
        await this.adminService.saveConfig(payload);

        this.config.emit(this.baseConfig);
    }

    async read(event: Event): Promise<void> {
        const target = (event.target as HTMLInputElement & EventTarget);

        const file = target.files?.item(0);

        if (!(file instanceof File)) return;

        const reader = new FileReader();

        reader.onloadend = () => {
            target.value = '';

            if (typeof reader.result !== 'string') {
                return;
            }

            this.csv = reader.result
                .split(/\n|\r\n/)
                .map((tg) => tg.replace(/^"|"$/g, '').split(/"*,"*/))
                .filter((tg) => tg && /^[0-9]+$/.test(tg[0]))
                .filter((tg, idx, arr) => arr.findIndex((a) => a[0] === tg[0]) === idx);
        };

        reader.readAsBinaryString(file);
    }
}
