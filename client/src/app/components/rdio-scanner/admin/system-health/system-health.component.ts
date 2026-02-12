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

import { Component, OnInit, OnDestroy, ChangeDetectorRef } from '@angular/core';
import { MatSnackBar } from '@angular/material/snack-bar';
import { RdioScannerAdminService } from '../admin.service';

export interface SystemAlert {
    id: number;
    alertType: string;
    severity: string;
    title: string;
    message: string;
    data: string;
    createdAt: number;
    createdBy: number;
    dismissed: boolean;
}

export interface FailedCall {
    callId: number;
    systemId: number;
    talkgroupId: number;
    timestamp: number;
    systemLabel: string;
    talkgroupLabel: string;
    talkgroupName: string;
    failureReason?: string;
}

@Component({
    selector: 'rdio-scanner-admin-system-health',
    styleUrls: ['./system-health.component.scss'],
    templateUrl: './system-health.component.html',
})
export class RdioScannerAdminSystemHealthComponent implements OnInit, OnDestroy {
    alerts: SystemAlert[] = [];
    loading = false;
    error: string | null = null;
    
    stats = {
        total: 0,
        critical: 0,
        error: 0,
        warning: 0,
        info: 0
    };

    // Transcription failure specific data
    failedCalls: FailedCall[] = [];
    loadingFailedCalls = false;
    
    // Systems list for per-system settings
    systems: any[] = [];
    loadingSystems = false;
    
    // System health alert settings
    settings = {
        transcriptionFailureAlertsEnabled: true,
        toneDetectionAlertsEnabled: true,
        noAudioAlertsEnabled: true,
        transcriptionFailureThreshold: 10,
        transcriptionFailureTimeWindow: 24,
        toneDetectionIssueThreshold: 5,
        toneDetectionTimeWindow: 24,
        noAudioThresholdMinutes: 30,
        noAudioMultiplier: 1.5,
        noAudioTimeWindow: 24,
        noAudioHistoricalDataDays: 7,
        transcriptionFailureRepeatMinutes: 60,
        toneDetectionRepeatMinutes: 60,
        noAudioRepeatMinutes: 30,
        alertRetentionDays: 5
    };
    
    // System health alerts enabled toggle
    systemHealthAlertsEnabled = true;
    
    // Options for dropdowns
    thresholdOptions = [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 15, 20, 25, 30, 40, 50, 75, 100];
    timeWindowOptions = [1, 2, 3, 4, 6, 8, 12, 24, 48, 72];
    multiplierOptions = [1.0, 1.2, 1.5, 2.0, 2.5, 3.0];
    historicalDaysOptions = [3, 5, 7, 10, 14, 21, 30];
    retentionDaysOptions = [1, 2, 3, 5, 7, 10, 14, 30];
    repeatMinutesOptions = [15, 30, 60, 120, 180, 240, 360, 480, 720];

    private refreshInterval: any;
    private saveFeedbackTimeouts: Map<string, any> = new Map();
    private saveFeedbackFields: Set<string> = new Set();

    constructor(
        private adminService: RdioScannerAdminService,
        private cdr: ChangeDetectorRef,
        private snackBar: MatSnackBar
    ) {}

    ngOnInit(): void {
        this.loadAlerts();
        this.loadSystemHealthAlertsEnabled();
        this.loadSystemHealthAlertSettings();
        this.loadSystems();
        // Auto-refresh every 60 seconds
        this.refreshInterval = setInterval(() => {
            this.loadAlerts();
        }, 60000);
    }

    ngOnDestroy(): void {
        if (this.refreshInterval) {
            clearInterval(this.refreshInterval);
        }
        // Clear all save feedback timeouts
        this.saveFeedbackTimeouts.forEach(timeout => clearTimeout(timeout));
        this.saveFeedbackTimeouts.clear();
        this.saveFeedbackFields.clear();
    }

    async loadAlerts(): Promise<void> {
        this.loading = true;
        this.error = null;

        try {
            const response = await this.adminService.getSystemHealth(100, false);
            this.alerts = response.alerts || [];
            this.updateStats();
            
            // Check if there's a transcription_failure alert and load failed calls
            const transcriptionFailureAlert = this.alerts.find(a => a.alertType === 'transcription_failure' && !a.dismissed);
            if (transcriptionFailureAlert) {
                this.loadFailedCalls();
            } else {
                this.failedCalls = [];
            }
        } catch (error: any) {
            this.error = error.message || 'Failed to load system health data';
            this.alerts = [];
        } finally {
            this.loading = false;
        }
    }

    async loadFailedCalls(): Promise<void> {
        this.loadingFailedCalls = true;
        try {
            const response = await this.adminService.getTranscriptionFailures();
            this.failedCalls = response.calls || [];
        } catch (error: any) {
            console.error('Failed to load failed calls:', error);
            this.failedCalls = [];
        } finally {
            this.loadingFailedCalls = false;
        }
    }


    async loadSystemHealthAlertsEnabled(): Promise<void> {
        try {
            this.systemHealthAlertsEnabled = await this.adminService.getSystemHealthAlertsEnabled();
        } catch (error: any) {
            console.error('Failed to load system health alerts enabled status:', error);
        }
    }

    async toggleSystemHealthAlertsEnabled(): Promise<void> {
        // Note: ngModel already changed the value, so use the current value
        const newValue = this.systemHealthAlertsEnabled;
        try {
            await this.adminService.setSystemHealthAlertsEnabled(newValue);
        } catch (error: any) {
            console.error('Failed to toggle system health alerts:', error);
            // Revert the toggle on error
            this.systemHealthAlertsEnabled = !newValue;
            alert('Failed to update setting: ' + (error.message || 'Unknown error'));
        }
    }

    async loadSystemHealthAlertSettings(): Promise<void> {
        try {
            const data = await this.adminService.getSystemHealthAlertSettings();
            this.settings = {
                transcriptionFailureAlertsEnabled: data.transcriptionFailureAlertsEnabled !== false,
                toneDetectionAlertsEnabled: data.toneDetectionAlertsEnabled !== false,
                noAudioAlertsEnabled: data.noAudioAlertsEnabled !== false,
                transcriptionFailureThreshold: data.transcriptionFailureThreshold || 10,
                transcriptionFailureTimeWindow: data.transcriptionFailureTimeWindow || 24,
                toneDetectionIssueThreshold: data.toneDetectionIssueThreshold || 5,
                toneDetectionTimeWindow: data.toneDetectionTimeWindow || 24,
                noAudioThresholdMinutes: data.noAudioThresholdMinutes || 30,
                noAudioMultiplier: data.noAudioMultiplier || 1.5,
                noAudioTimeWindow: data.noAudioTimeWindow || 24,
                noAudioHistoricalDataDays: data.noAudioHistoricalDataDays || 7,
                transcriptionFailureRepeatMinutes: data.transcriptionFailureRepeatMinutes || 60,
                toneDetectionRepeatMinutes: data.toneDetectionRepeatMinutes || 60,
                noAudioRepeatMinutes: data.noAudioRepeatMinutes || 30,
                alertRetentionDays: data.alertRetentionDays || 5
            };
        } catch (error: any) {
            console.error('Failed to load system health alert settings:', error);
        }
    }

    async saveSetting(field: string, value: any): Promise<void> {
        try {
            const update: any = {};
            update[field] = value;
            await this.adminService.updateSystemHealthAlertSettings(update);
            (this.settings as any)[field] = value;
            // Show brief success feedback
            this.showSaveFeedback(field);
            // Show success message
            this.snackBar.open('Setting saved', '', {
                duration: 2000,
                horizontalPosition: 'center',
                verticalPosition: 'bottom',
                panelClass: ['success-snackbar']
            });
            // Trigger change detection to show the feedback icon
            this.cdr.detectChanges();
        } catch (error: any) {
            console.error(`Failed to save ${field}:`, error);
            this.snackBar.open(`Failed to save setting: ${error.message || 'Unknown error'}`, 'Close', {
                duration: 4000,
                horizontalPosition: 'center',
                verticalPosition: 'bottom',
                panelClass: ['error-snackbar']
            });
        }
    }

    showSaveFeedback(field: string): void {
        // Add field to feedback set
        this.saveFeedbackFields.add(field);
        
        // Clear existing timeout for this field
        if (this.saveFeedbackTimeouts.has(field)) {
            clearTimeout(this.saveFeedbackTimeouts.get(field));
        }
        
        // Remove feedback after 2 seconds
        const timeout = setTimeout(() => {
            this.saveFeedbackFields.delete(field);
            this.saveFeedbackTimeouts.delete(field);
            this.cdr.detectChanges();
        }, 2000);
        
        this.saveFeedbackTimeouts.set(field, timeout);
    }

    hasSaveFeedback(field: string): boolean {
        return this.saveFeedbackFields.has(field);
    }


    async resetFailures(callIds?: number[]): Promise<void> {
        if (!confirm(callIds ? 'Reset selected transcription failures?' : 'Reset all transcription failures from the last 24 hours?')) {
            return;
        }

        try {
            await this.adminService.resetTranscriptionFailures(callIds);
            await this.loadFailedCalls();
            await this.loadAlerts(); // Reload alerts to update count
        } catch (error: any) {
            console.error('Failed to reset failures:', error);
            alert('Failed to reset failures: ' + (error.message || 'Unknown error'));
        }
    }

    getTranscriptionFailureAlert(): SystemAlert | undefined {
        return this.alerts.find(a => a.alertType === 'transcription_failure' && !a.dismissed);
    }

    private updateStats(): void {
        const activeAlerts = this.alerts.filter(a => !a.dismissed);
        this.stats = {
            total: activeAlerts.length,
            critical: 0,
            error: 0,
            warning: 0,
            info: 0
        };

        activeAlerts.forEach(alert => {
            if (alert.severity === 'critical') this.stats.critical++;
            else if (alert.severity === 'error') this.stats.error++;
            else if (alert.severity === 'warning') this.stats.warning++;
            else if (alert.severity === 'info') this.stats.info++;
        });
    }

    getGroupedAlerts(): { [key: string]: SystemAlert[] } {
        const activeAlerts = this.alerts.filter(a => !a.dismissed);
        const grouped: { [key: string]: SystemAlert[] } = {
            'no_audio_received': [],
            'tone_detection_issue': [],
            'transcription_failure': [],
            'other': []
        };

        for (const alert of activeAlerts) {
            if (alert.alertType in grouped) {
                grouped[alert.alertType].push(alert);
            } else {
                grouped['other'].push(alert);
            }
        }

        // Remove empty groups
        Object.keys(grouped).forEach(key => {
            if (grouped[key].length === 0) {
                delete grouped[key];
            }
        });

        return grouped;
    }

    getGroupedAlertKeys(): string[] {
        return Object.keys(this.getGroupedAlerts());
    }

    getAlertTypeLabel(type: string): string {
        switch (type) {
            case 'no_audio_received':
                return 'No Audio Received';
            case 'tone_detection_issue':
                return 'Tone Detection Issues';
            case 'transcription_failure':
                return 'Transcription Failures';
            default:
                return 'Other Alerts';
        }
    }

    async dismissAlert(alertId: number): Promise<void> {
        try {
            await this.adminService.dismissSystemAlert(alertId);
            // Reload alerts to refresh the display
            await this.loadAlerts();
            this.snackBar.open('Alert dismissed', '', {
                duration: 2000,
                horizontalPosition: 'center',
                verticalPosition: 'bottom',
                panelClass: ['success-snackbar']
            });
        } catch (error: any) {
            console.error('Failed to dismiss alert:', error);
            this.snackBar.open(`Failed to dismiss alert: ${error.message || 'Unknown error'}`, 'Close', {
                duration: 4000,
                horizontalPosition: 'center',
                verticalPosition: 'bottom',
                panelClass: ['error-snackbar']
            });
        }
    }

    async dismissAllAlertsInGroup(groupType: string): Promise<void> {
        const groupedAlerts = this.getGroupedAlerts();
        const alertsInGroup = groupedAlerts[groupType] || [];
        
        if (alertsInGroup.length === 0) {
            return;
        }

        const groupLabel = this.getAlertTypeLabel(groupType);
        if (!confirm(`Dismiss all ${alertsInGroup.length} alert${alertsInGroup.length !== 1 ? 's' : ''} in "${groupLabel}"?`)) {
            return;
        }

        try {
            // Dismiss all alerts in parallel
            const dismissPromises = alertsInGroup.map(alert => 
                this.adminService.dismissSystemAlert(alert.id)
            );
            await Promise.all(dismissPromises);
            
            // Reload alerts to refresh the display
            await this.loadAlerts();
            
            this.snackBar.open(`${alertsInGroup.length} alert${alertsInGroup.length !== 1 ? 's' : ''} dismissed`, '', {
                duration: 2000,
                horizontalPosition: 'center',
                verticalPosition: 'bottom',
                panelClass: ['success-snackbar']
            });
        } catch (error: any) {
            console.error('Failed to dismiss alerts:', error);
            this.snackBar.open(`Failed to dismiss alerts: ${error.message || 'Unknown error'}`, 'Close', {
                duration: 4000,
                horizontalPosition: 'center',
                verticalPosition: 'bottom',
                panelClass: ['error-snackbar']
            });
        }
    }

    getSeverityIcon(severity: string): string {
        switch (severity) {
            case 'critical': return '🚨';
            case 'error': return '❌';
            case 'warning': return '⚠️';
            case 'info': return 'ℹ️';
            default: return '📋';
        }
    }

    getAlertData(alert: SystemAlert): any {
        try {
            return JSON.parse(alert.data || '{}');
        } catch {
            return {};
        }
    }

    hasAlertData(alert: SystemAlert): boolean {
        const data = this.getAlertData(alert);
        return data && Object.keys(data).length > 0;
    }

    formatDate(timestamp: number): string {
        return new Date(timestamp).toLocaleString();
    }

    playingCallId: number | null = null;
    audioElement: HTMLAudioElement | null = null;

    playCall(callId: number): void {
        // If already playing this call, stop it
        if (this.playingCallId === callId && this.audioElement) {
            this.audioElement.pause();
            this.audioElement = null;
            this.playingCallId = null;
            return;
        }

        // Stop any currently playing audio
        if (this.audioElement) {
            this.audioElement.pause();
            this.audioElement = null;
        }

        // Create new audio element
        const audio = new Audio();
        const audioUrl = this.adminService.getCallAudioUrl(callId);
        
        // Add authorization token to the URL as a query parameter
        // Since we can't set headers on Audio element, we'll need to use a different approach
        // Actually, we can use a blob URL by fetching with headers first
        this.loadAndPlayAudio(callId, audio);
    }

    async loadAndPlayAudio(callId: number, audio: HTMLAudioElement): Promise<void> {
        try {
            // Fetch audio with authentication headers
            const audioUrl = this.adminService.getCallAudioUrl(callId);
            const headers = this.adminService.getFetchHeaders();
            const response = await fetch(audioUrl, {
                headers: headers,
            });

            if (!response.ok) {
                throw new Error(`Failed to load audio: ${response.statusText}`);
            }

            const blob = await response.blob();
            const blobUrl = URL.createObjectURL(blob);
            
            audio.src = blobUrl;
            audio.onended = () => {
                URL.revokeObjectURL(blobUrl);
                this.audioElement = null;
                this.playingCallId = null;
            };
            audio.onerror = () => {
                URL.revokeObjectURL(blobUrl);
                this.audioElement = null;
                this.playingCallId = null;
                alert('Failed to play audio');
            };

            this.audioElement = audio;
            this.playingCallId = callId;
            await audio.play();
        } catch (error: any) {
            console.error('Failed to play call audio:', error);
            alert('Failed to play audio: ' + (error.message || 'Unknown error'));
            this.audioElement = null;
            this.playingCallId = null;
        }
    }

    isPlaying(callId: number): boolean {
        return this.playingCallId === callId && this.audioElement !== null && !this.audioElement.paused;
    }

    async loadSystems(): Promise<void> {
        this.loadingSystems = true;
        try {
            const config = await this.adminService.getConfig();
            this.systems = config.systems || [];
        } catch (error: any) {
            console.error('Failed to load systems:', error);
            this.snackBar.open('Failed to load systems', 'Close', {
                duration: 4000,
                horizontalPosition: 'center',
                verticalPosition: 'bottom'
            });
        } finally {
            this.loadingSystems = false;
        }
    }

    async saveSystemNoAudioSetting(system: any): Promise<void> {
        try {
            // Use dedicated endpoint to update just this system's settings
            await this.adminService.saveSystemNoAudioSettings(
                system.id,
                system.noAudioAlertsEnabled || false,
                system.noAudioThresholdMinutes || 30
            );
            this.snackBar.open(`Updated no-audio settings for ${system.label}`, '', {
                duration: 2000,
                horizontalPosition: 'center',
                verticalPosition: 'bottom',
                panelClass: ['success-snackbar']
            });
        } catch (error: any) {
            console.error(`Failed to save system settings:`, error);
            this.snackBar.open(`Failed to save: ${error.message || 'Unknown error'}`, 'Close', {
                duration: 4000,
                horizontalPosition: 'center',
                verticalPosition: 'bottom'
            });
            // Reload systems to revert UI
            await this.loadSystems();
        }
    }
}

