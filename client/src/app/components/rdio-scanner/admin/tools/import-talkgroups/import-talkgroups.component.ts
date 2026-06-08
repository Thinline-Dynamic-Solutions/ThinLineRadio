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
    selector: 'rdio-scanner-admin-import-talkgroups',
    styleUrls: ['./import-talkgroups.component.scss'],
    templateUrl: './import-talkgroups.component.html',
})
export class RdioScannerAdminImportTalkgroupsComponent implements OnInit {
    @Output() config = new EventEmitter<Config>();

    baseConfig: Config = {};

    csv: string[][] = [];

    system: System | undefined;

    tableColumns = ['id', 'label', 'description', 'tag', 'group', 'action'];

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
        const system = this.system;
        if (system === undefined) return;

        if (!this.baseConfig.groups) {
            this.baseConfig.groups = [];
        }

        if (!this.baseConfig.tags) {
            this.baseConfig.tags = [];
        }

        if (!system.talkgroups) {
            system.talkgroups = [];
        }
        const talkgroups = system.talkgroups;

        this.csv.forEach((csv) => {
            const group = csv[6];

            if (!this.baseConfig.groups?.find((g) => g.label === group)) {
                const id = this.baseConfig.groups?.reduce((pv, cv) => typeof cv.id === 'number' && cv.id >= pv ? cv.id + 1 : pv, 1);

                this.baseConfig.groups?.push({ id: id, label: group });
            }

            const tag = csv[5];

            if (!this.baseConfig.tags?.find((t) => t.label === tag)) {
                const id = this.baseConfig.tags?.reduce((pv, cv) => typeof cv.id === 'number' && cv.id >= pv ? cv.id + 1 : pv, 1);

                this.baseConfig.tags?.push({ id: id, label: tag });
            }
        });

        this.csv.forEach((csv) => {
            const groupId = this.baseConfig.groups?.find((g) => g.label === csv[6])?.id;
            const tagId = this.baseConfig.tags?.find((t) => t.label === csv[5])?.id;

            talkgroups.push({
                talkgroupRef: +csv[0],
                label: csv[2],
                name: csv[4],
                groupIds: groupId ? [groupId] : [],
                tagId,
            });
        });

        this.csv = [];

        // Persist directly — the global Save bar was removed. Bulk imports are a
        // whole-config write (the loaded config holds every system/talkgroup).
        // Strip the user-scoped collections so we never clobber them; they are
        // managed by their own dedicated endpoints.
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
