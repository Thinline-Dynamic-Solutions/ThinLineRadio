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

import { Component, OnInit, ViewChild } from '@angular/core';
import { FormBuilder, FormGroup } from '@angular/forms';
import { MatDatepicker } from '@angular/material/datepicker';
import { MatPaginator } from '@angular/material/paginator';
import { BehaviorSubject } from 'rxjs';
import { Log, LogCategory, LogsQuery, LogsQueryOptions, RdioScannerAdminService } from '../admin.service';

@Component({
    selector: 'rdio-scanner-admin-logs',
    styleUrls: ['./logs.component.scss'],
    templateUrl: './logs.component.html',
})
export class RdioScannerAdminLogsComponent implements OnInit {
    /** Rows fetched per paginator page (scroll inside the table to see all of them). */
    readonly pageSize = 50;

    form: FormGroup;

    logs = new BehaviorSubject(new Array<Log | null>(this.pageSize));

    logsQuery: LogsQuery | undefined = undefined;

    logsQueryPending = false;

    logCategories: LogCategory[] = [];

    selectedCategories = new Set<string>();

    selectedDate: Date | null = null;

    /** HH:mm on `selectedDate`; midnight when only a date is chosen. */
    selectedTime: string | null = null;

    private limit = this.pageSize;

    private offset = 0;

    @ViewChild(MatPaginator) private paginator: MatPaginator | undefined;

    @ViewChild('logDatePicker') private logDatePicker: MatDatepicker<Date> | undefined;

    constructor(private adminService: RdioScannerAdminService, private ngFormBuilder: FormBuilder) {
        this.form = this.ngFormBuilder.group({
            level: [null],
            search: [''],
            sort: [-1],
        });
    }

    async ngOnInit(): Promise<void> {
        this.logCategories = await this.adminService.getLogCategories();
        const allowed = new Set(this.logCategories.map((c) => c.key));
        for (const key of Array.from(this.selectedCategories)) {
            if (!allowed.has(key)) {
                this.selectedCategories.delete(key);
            }
        }
    }

    openDatePicker(): void {
        this.logDatePicker?.open();
    }

    onDateSelected(event: { value: Date | null }): void {
        const date = event?.value;
        if (date instanceof Date) {
            this.selectedDate = new Date(date.getFullYear(), date.getMonth(), date.getDate(), 0, 0, 0, 0);
            this.form.markAsDirty();
            this.formHandler();
            return;
        }
        if (date === null) {
            this.clearDate();
        }
    }

    clearDate(): void {
        this.selectedDate = null;
        this.selectedTime = null;
        this.form.markAsDirty();
        this.formHandler();
    }

    getHour(): number {
        if (!this.selectedTime) {
            return 0;
        }
        const [h] = this.selectedTime.split(':');
        return parseInt(h, 10) || 0;
    }

    getMinute(): number {
        if (!this.selectedTime) {
            return 0;
        }
        const [, m] = this.selectedTime.split(':');
        return parseInt(m, 10) || 0;
    }

    pad2(n: number): string {
        return String(n).padStart(2, '0');
    }

    getTimeDisplay(): string {
        return `${this.pad2(this.getHour())}:${this.pad2(this.getMinute())}`;
    }

    private setTime(hour: number, minute: number): void {
        const h = ((hour % 24) + 24) % 24;
        const m = ((minute % 60) + 60) % 60;
        this.selectedTime = `${this.pad2(h)}:${this.pad2(m)}`;
        this.form.markAsDirty();
        this.formHandler();
    }

    bumpHour(delta: number): void {
        this.setTime(this.getHour() + delta, this.getMinute());
    }

    bumpMinute(delta: number): void {
        this.setTime(this.getHour(), this.getMinute() + delta);
    }

    setTimeNow(): void {
        const now = new Date();
        this.setTime(now.getHours(), now.getMinutes());
    }

    clearTime(): void {
        this.selectedTime = null;
        this.form.markAsDirty();
        this.formHandler();
    }

    isCategorySelected(key: string): boolean {
        return this.selectedCategories.has(key);
    }

    toggleCategory(key: string): void {
        if (this.selectedCategories.has(key)) {
            this.selectedCategories.delete(key);
        } else {
            this.selectedCategories.add(key);
        }
        this.form.markAsDirty();
        this.formHandler();
    }

    clearCategories(): void {
        if (this.selectedCategories.size === 0) {
            return;
        }
        this.selectedCategories.clear();
        this.form.markAsDirty();
        this.formHandler();
    }

    categoryLabel(key: string | undefined): string {
        if (!key) {
            return '';
        }
        const found = this.logCategories.find((c) => c.key === key);
        return found?.label ?? key;
    }

    levelLabel(level: string | undefined): string {
        switch (level) {
            case 'error':
                return 'Error';
            case 'warn':
                return 'Warn';
            case 'info':
                return 'Info';
            default:
                return level ?? '';
        }
    }

    private buildFilterDate(): Date | undefined {
        if (!this.selectedDate) {
            return undefined;
        }
        return new Date(
            this.selectedDate.getFullYear(),
            this.selectedDate.getMonth(),
            this.selectedDate.getDate(),
            this.selectedTime ? this.getHour() : 0,
            this.selectedTime ? this.getMinute() : 0,
            0,
            0,
        );
    }

    formHandler(): void {
        this.paginator?.firstPage();
        void this.reload();
    }

    reset(): void {
        this.selectedDate = null;
        this.selectedTime = null;
        this.selectedCategories.clear();

        this.form.reset({
            level: null,
            search: '',
            sort: -1,
        });

        this.formHandler();
    }

    refresh(): void {
        if (!this.paginator) {
            return;
        }

        const from = this.paginator.pageIndex * this.paginator.pageSize;

        const to = this.paginator.pageIndex * this.paginator.pageSize + this.paginator.pageSize - 1;

        if (!this.logsQueryPending && (from >= this.offset + this.limit || from < this.offset)) {
            void this.reload();
        } else if (this.logsQuery) {
            const logs: Array<Log | null> = this.logsQuery.logs.slice(from % this.limit, to % this.limit + 1);

            while (logs.length < this.logs.value.length) {
                logs.push(null);
            }

            this.logs.next(logs);
        }
    }

    async reload(): Promise<void> {
        const pageIndex = this.paginator?.pageIndex || 0;

        const pageSize = this.paginator?.pageSize || this.pageSize;

        this.offset = Math.floor((pageIndex * pageSize) / this.limit) * this.limit;

        const options: LogsQueryOptions = {
            limit: this.limit,
            offset: this.offset,
            sort: this.form.get('sort')?.value ?? -1,
        };

        const level = this.form.get('level')?.value;
        if (level === 'info' || level === 'warn' || level === 'error') {
            options.level = level;
        }

        const search = (this.form.get('search')?.value ?? '').trim();
        if (search) {
            options.search = search;
        }

        if (this.selectedCategories.size > 0) {
            options.categories = Array.from(this.selectedCategories);
        }

        const filterDate = this.buildFilterDate();
        if (filterDate) {
            options.date = filterDate;
        }

        this.logsQueryPending = true;

        this.form.disable();

        this.logsQuery = await this.adminService.getLogs(options);

        this.form.enable();

        this.logsQueryPending = false;

        this.refresh();
    }
}
