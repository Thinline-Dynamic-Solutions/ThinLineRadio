import { Component, ViewChild } from '@angular/core';
import { FormBuilder, FormGroup } from '@angular/forms';
import { MatPaginator } from '@angular/material/paginator';
import { MatSnackBar } from '@angular/material/snack-bar';
import { BehaviorSubject } from 'rxjs';
import { Log, LogsQuery, LogsQueryOptions, CallSearchResult, CallsQuery, CallsQueryOptions, RdioScannerAdminService, Config } from '../../admin.service';

@Component({
    selector: 'rdio-scanner-admin-purge-data',
    templateUrl: './purge-data.component.html',
    styleUrls: ['./purge-data.component.scss'],
    standalone: false
})
export class RdioScannerAdminPurgeDataComponent {
    purgingCalls = false;
    purgingLogs = false;
    
    // Selective delete for logs
    logs = new BehaviorSubject(new Array<Log | null>(10));
    logsQuery: LogsQuery | undefined = undefined;
    logsQueryPending = false;
    selectedLogIds = new Set<number>();
    showSelectiveLogs = false;
    
    private logsLimit = 200;
    private logsOffset = 0;
    @ViewChild('logsPaginator') logsPaginator: MatPaginator | undefined;
    logsForm: FormGroup;

    constructor(
        private adminService: RdioScannerAdminService,
        private snackBar: MatSnackBar,
        private formBuilder: FormBuilder
    ) {
        this.logsForm = this.formBuilder.group({
            date: [null],
            level: [null],
            sort: [-1],
        });
        this.callsForm = this.formBuilder.group({
            date: [null],
            system: [null],
            talkgroup: [null],
            sort: [-1],
        });
    }

    async purgeCalls(): Promise<void> {
        if (this.purgingCalls) {
            return;
        }

        // Single confirmation with typed confirmation
        const typedConfirm = prompt(
            'WARNING: This will permanently delete ALL calls from the database.\n\n' +
            'This will remove all audio recordings, transcripts, and call metadata.\n' +
            'This action cannot be undone.\n\n' +
            'Please type "PURGE ALL CALLS" (without quotes) to confirm deletion:'
        );

        if (typedConfirm !== 'PURGE ALL CALLS') {
            if (typedConfirm !== null) {
                this.snackBar.open('Purge cancelled. Confirmation text did not match.', 'Close', {
                    duration: 3000
                });
            }
            return;
        }

        this.purgingCalls = true;

        try {
            const result = await this.adminService.purgeData('calls');
            this.snackBar.open(
                result.message || 'All calls purged successfully',
                'Close',
                { duration: 5000, panelClass: ['success-snackbar'] }
            );
        } catch (error: any) {
            this.snackBar.open(
                error?.error?.error || 'Failed to purge calls',
                'Close',
                { duration: 5000, panelClass: ['error-snackbar'] }
            );
        } finally {
            this.purgingCalls = false;
        }
    }

    async purgeLogs(): Promise<void> {
        if (this.purgingLogs) {
            return;
        }

        // Single confirmation with typed confirmation
        const typedConfirm = prompt(
            'WARNING: This will permanently delete ALL logs from the database.\n\n' +
            'This will remove all system log entries including errors, warnings, and info messages.\n' +
            'This action cannot be undone.\n\n' +
            'Please type "PURGE ALL LOGS" (without quotes) to confirm deletion:'
        );

        if (typedConfirm !== 'PURGE ALL LOGS') {
            if (typedConfirm !== null) {
                this.snackBar.open('Purge cancelled. Confirmation text did not match.', 'Close', {
                    duration: 3000
                });
            }
            return;
        }

        this.purgingLogs = true;

        try {
            const result = await this.adminService.purgeData('logs');
            this.snackBar.open(
                result.message || 'All logs purged successfully',
                'Close',
                { duration: 5000, panelClass: ['success-snackbar'] }
            );
        } catch (error: any) {
            this.snackBar.open(
                error?.error?.error || 'Failed to purge logs',
                'Close',
                { duration: 5000, panelClass: ['error-snackbar'] }
            );
        } finally {
            this.purgingLogs = false;
        }
    }

    // Selective delete methods for logs
    toggleLogsSelection(): void {
        // This is handled by mat-expansion-panel [(expanded)] binding
    }

    logsPanelOpened(): void {
        if (!this.logsQuery && !this.logsQueryPending) {
            this.reloadLogs();
        } else if (this.logsQuery) {
            this.refreshLogs();
        }
    }

    toggleLogSelection(logId: number | undefined): void {
        if (logId === undefined) return;
        if (this.selectedLogIds.has(logId)) {
            this.selectedLogIds.delete(logId);
        } else {
            this.selectedLogIds.add(logId);
        }
    }

    isLogSelected(logId: number | undefined): boolean {
        return logId !== undefined && this.selectedLogIds.has(logId);
    }

    selectAllLogsOnPage(): void {
        if (!this.logsPaginator || !this.logsQuery) return;
        const from = this.logsPaginator.pageIndex * this.logsPaginator.pageSize;
        const to = Math.min(from + this.logsPaginator.pageSize, this.logsQuery.logs.length);
        for (let i = from; i < to; i++) {
            const log = this.logsQuery.logs[i];
            if (log && log.id !== undefined) {
                this.selectedLogIds.add(log.id);
            }
        }
    }

    selectAllLogsInBatch(): void {
        if (!this.logsQuery) return;
        this.logsQuery.logs.forEach(log => {
            if (log.id !== undefined) {
                this.selectedLogIds.add(log.id);
            }
        });
    }

    deselectAllLogs(): void {
        this.selectedLogIds.clear();
    }

    async reloadLogs(): Promise<void> {
        // Use default page size if paginator not ready
        const pageIndex = this.logsPaginator?.pageIndex || 0;
        const pageSize = this.logsPaginator?.pageSize || 10;
        this.logsOffset = Math.floor((pageIndex * pageSize) / this.logsLimit) * this.logsLimit;

        const options: LogsQueryOptions = {
            limit: this.logsLimit,
            offset: this.logsOffset,
            sort: this.logsForm.get('sort')?.value ?? -1,
        };

        if (typeof this.logsForm.get('level')?.value === 'string') {
            options.level = this.logsForm.get('level')?.value ?? undefined;
        }

        const dateValue = this.logsForm.get('date')?.value;
        if (dateValue) {
            if (typeof dateValue === 'string' && dateValue.length > 0) {
                options.date = new Date(Date.parse(dateValue));
            } else if (dateValue instanceof Date) {
                options.date = dateValue;
            }
        }

        this.logsQueryPending = true;
        this.logsForm.disable();

        try {
            this.logsQuery = await this.adminService.getLogs(options);
        } catch (error) {
            console.error('Failed to load logs:', error);
            this.logsQuery = undefined;
        }

        this.logsForm.enable();
        this.logsQueryPending = false;

        this.refreshLogs();
    }

    refreshLogs(): void {
        if (!this.logsPaginator) {
            return;
        }

        const from = this.logsPaginator.pageIndex * this.logsPaginator.pageSize;
        const to = this.logsPaginator.pageIndex * this.logsPaginator.pageSize + this.logsPaginator.pageSize - 1;

        if (!this.logsQueryPending && (from >= this.logsOffset + this.logsLimit || from < this.logsOffset)) {
            this.reloadLogs();
        } else if (this.logsQuery) {
            const logs: Array<Log | null> = this.logsQuery.logs.slice(from % this.logsLimit, to % this.logsLimit + 1);
            while (logs.length < this.logs.value.length) {
                logs.push(null);
            }
            this.logs.next(logs);
        }
    }

    async deleteSelectedLogs(): Promise<void> {
        if (this.selectedLogIds.size === 0) {
            this.snackBar.open('No logs selected', 'Close', { duration: 3000 });
            return;
        }

        const confirmed = confirm(
            `Are you sure you want to delete ${this.selectedLogIds.size} selected log${this.selectedLogIds.size !== 1 ? 's' : ''}?\n\n` +
            'This action cannot be undone.'
        );

        if (!confirmed) {
            return;
        }

        this.purgingLogs = true;
        const ids = Array.from(this.selectedLogIds);

        try {
            const result = await this.adminService.purgeData('logs', ids);
            this.snackBar.open(
                result.message || `${ids.length} logs deleted successfully`,
                'Close',
                { duration: 5000, panelClass: ['success-snackbar'] }
            );
            this.selectedLogIds.clear();
            await this.reloadLogs();
        } catch (error: any) {
            this.snackBar.open(
                error?.error?.error || 'Failed to delete logs',
                'Close',
                { duration: 5000, panelClass: ['error-snackbar'] }
            );
        } finally {
            this.purgingLogs = false;
        }
    }

    // Selective delete methods for calls
    calls = new BehaviorSubject(new Array<CallSearchResult | null>(10));
    callsQuery: CallsQuery | undefined = undefined;
    callsQueryPending = false;
    selectedCallIds = new Set<number>();
    showSelectiveCalls = false;
    config: Config | undefined;
    
    private callsLimit = 200;
    private callsOffset = 0;
    @ViewChild('callsPaginator') callsPaginator: MatPaginator | undefined;
    callsForm: FormGroup;

    toggleCallsSelection(): void {
        // This is handled by mat-expansion-panel [(expanded)] binding
    }

    callsPanelOpened(): void {
        if (!this.callsQuery && !this.callsQueryPending) {
            this.loadConfigIfNeeded().then(() => this.reloadCalls());
        } else if (this.callsQuery) {
            this.refreshCalls();
        }
    }

    async loadConfigIfNeeded(): Promise<void> {
        if (!this.config) {
            this.config = await this.adminService.getConfig();
        }
    }

    async deleteSelectedCalls(): Promise<void> {
        if (this.selectedCallIds.size === 0) {
            this.snackBar.open('No calls selected', 'Close', { duration: 3000 });
            return;
        }

        const confirmed = confirm(
            `Are you sure you want to delete ${this.selectedCallIds.size} selected call${this.selectedCallIds.size !== 1 ? 's' : ''}?\n\n` +
            'This will permanently delete the selected calls including audio recordings, transcripts, and metadata.\n\n' +
            'This action cannot be undone.'
        );

        if (!confirmed) {
            return;
        }

        this.purgingCalls = true;
        const ids = Array.from(this.selectedCallIds);

        try {
            const result = await this.adminService.purgeData('calls', ids);
            this.snackBar.open(
                result.message || `${ids.length} calls deleted successfully`,
                'Close',
                { duration: 5000, panelClass: ['success-snackbar'] }
            );
            this.selectedCallIds.clear();
            await this.reloadCalls();
        } catch (error: any) {
            this.snackBar.open(
                error?.error?.error || 'Failed to delete calls',
                'Close',
                { duration: 5000, panelClass: ['error-snackbar'] }
            );
        } finally {
            this.purgingCalls = false;
        }
    }

    toggleCallSelection(callId: number | undefined): void {
        if (callId === undefined) return;
        if (this.selectedCallIds.has(callId)) {
            this.selectedCallIds.delete(callId);
        } else {
            this.selectedCallIds.add(callId);
        }
    }

    isCallSelected(callId: number | undefined): boolean {
        return callId !== undefined && this.selectedCallIds.has(callId);
    }

    selectAllCallsOnPage(): void {
        if (!this.callsPaginator || !this.callsQuery) return;
        const from = this.callsPaginator.pageIndex * this.callsPaginator.pageSize;
        const to = Math.min(from + this.callsPaginator.pageSize, this.callsQuery.results.length);
        for (let i = from; i < to; i++) {
            const call = this.callsQuery.results[i];
            if (call && call.id !== undefined) {
                this.selectedCallIds.add(call.id);
            }
        }
    }

    selectAllCallsInBatch(): void {
        if (!this.callsQuery) return;
        this.callsQuery.results.forEach(call => {
            if (call.id !== undefined) {
                this.selectedCallIds.add(call.id);
            }
        });
    }

    deselectAllCalls(): void {
        this.selectedCallIds.clear();
    }

    async reloadCalls(): Promise<void> {
        const pageIndex = this.callsPaginator?.pageIndex || 0;
        const pageSize = this.callsPaginator?.pageSize || 10;
        this.callsOffset = Math.floor((pageIndex * pageSize) / this.callsLimit) * this.callsLimit;

        const options: CallsQueryOptions = {
            limit: this.callsLimit,
            offset: this.callsOffset,
            sort: this.callsForm.get('sort')?.value ?? -1,
        };

        const dateValue = this.callsForm.get('date')?.value;
        if (dateValue) {
            if (typeof dateValue === 'string' && dateValue.length > 0) {
                options.date = new Date(Date.parse(dateValue));
            } else if (dateValue instanceof Date) {
                options.date = dateValue;
            }
        }

        const systemValue = this.callsForm.get('system')?.value;
        if (systemValue !== null && systemValue !== undefined && systemValue !== -1) {
            const system = this.config?.systems?.find((s: any) => s.id === systemValue);
            if (system?.systemRef) {
                options.system = system.systemRef;
            }
        }

        const talkgroupValue = this.callsForm.get('talkgroup')?.value;
        if (talkgroupValue !== null && talkgroupValue !== undefined && talkgroupValue !== -1) {
            const selectedSystem = this.callsForm.get('system')?.value;
            const system = this.config?.systems?.find((s: any) => s.id === selectedSystem);
            const talkgroup = system?.talkgroups?.find((tg: any) => tg.id === talkgroupValue);
            if (talkgroup?.talkgroupRef) {
                options.talkgroup = talkgroup.talkgroupRef;
            }
        }

        this.callsQueryPending = true;
        this.callsForm.disable();

        try {
            this.callsQuery = await this.adminService.getCalls(options);
        } catch (error) {
            console.error('Failed to load calls:', error);
            this.callsQuery = undefined;
        }

        this.callsForm.enable();
        this.callsQueryPending = false;

        this.refreshCalls();
    }

    refreshCalls(): void {
        if (!this.callsPaginator) {
            return;
        }

        const from = this.callsPaginator.pageIndex * this.callsPaginator.pageSize;
        const to = this.callsPaginator.pageIndex * this.callsPaginator.pageSize + this.callsPaginator.pageSize - 1;

        if (!this.callsQueryPending && (from >= this.callsOffset + this.callsLimit || from < this.callsOffset)) {
            this.reloadCalls();
        } else if (this.callsQuery) {
            const calls: Array<CallSearchResult | null> = this.callsQuery.results.slice(from % this.callsLimit, to % this.callsLimit + 1);
            while (calls.length < this.calls.value.length) {
                calls.push(null);
            }
            this.calls.next(calls);
        }
    }

    getSystems(): any[] {
        return this.config?.systems || [];
    }

    getTalkgroups(systemId: number): any[] {
        if (!systemId || !this.config?.systems) return [];
        const system = this.config.systems.find((s: any) => s.id === systemId);
        return system?.talkgroups || [];
    }

    getSystemLabel(systemRef: number): string {
        if (!this.config?.systems) return '';
        const system = this.config.systems.find((s: any) => s.systemRef === systemRef);
        return system?.label || '';
    }

    getTalkgroupLabel(systemRef: number, talkgroupRef: number): string {
        if (!this.config?.systems) return '';
        const system = this.config.systems.find((s: any) => s.systemRef === systemRef);
        const talkgroup = system?.talkgroups?.find((tg: any) => tg.talkgroupRef === talkgroupRef);
        return talkgroup?.label || '';
    }

}

