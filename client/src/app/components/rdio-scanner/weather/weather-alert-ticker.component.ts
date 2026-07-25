/*
 * Copyright (C) 2025 Thinline Dynamic Solutions
 */

import {
    ChangeDetectionStrategy,
    ChangeDetectorRef,
    Component,
    OnDestroy,
    OnInit,
} from '@angular/core';
import { Subscription, interval, switchMap, of, catchError } from 'rxjs';
import { AlertSoundService } from '../alert-sound.service';
import { SettingsService } from '../settings/settings.service';
import { NwsSevereAlert, NwsService } from './nws.service';
import { WeatherAlertTickerBridgeService } from './weather-alert-ticker-bridge.service';
import { WeatherAlertTtsService } from './weather-alert-tts.service';

const TEST_ALERT_DURATION_MS = 25_000;

@Component({
    selector: 'rdio-scanner-weather-alert-ticker',
    templateUrl: './weather-alert-ticker.component.html',
    styleUrls: ['./weather-alert-ticker.component.scss'],
    changeDetection: ChangeDetectionStrategy.OnPush,
})
export class RdioScannerWeatherAlertTickerComponent implements OnInit, OnDestroy {
    alerts: NwsSevereAlert[] = [];
    tickerText = '';
    hasZip = false;
    loading = false;
    testMode = false;
    flashing = false;

    private subs: Subscription[] = [];
    private knownAlertIds = new Set<string>();
    private settingsSnapshot: Record<string, unknown> = {};
    private testClearTimer: ReturnType<typeof setTimeout> | null = null;

    constructor(
        private nwsService: NwsService,
        private settingsService: SettingsService,
        private alertSoundService: AlertSoundService,
        private tickerBridge: WeatherAlertTickerBridgeService,
        private weatherAlertTtsService: WeatherAlertTtsService,
        private cdr: ChangeDetectorRef,
    ) {}

    ngOnInit(): void {
        this.tickerBridge.register(this);
        this.settingsService.getSettings().subscribe();
        this.subs.push(
            this.nwsService.getZipCode().subscribe((zip) => {
                this.hasZip = !!zip;
                if (zip) {
                    this.knownAlertIds.clear();
                    this.refresh();
                } else {
                    this.alerts = [];
                    this.tickerText = '';
                }
                this.cdr.markForCheck();
            }),
            this.settingsService.watchSettings().subscribe({
                next: (settings) => {
                    this.settingsSnapshot = settings || {};
                    this.cdr.markForCheck();
                },
                error: () => { /* optional */ },
            }),
            interval(5 * 60 * 1000).pipe(
                switchMap(() => this.nwsService.fetchSevereAlertsForUserArea()),
            ).subscribe((alerts) => this.applyAlerts(alerts)),
        );
    }

    ngOnDestroy(): void {
        this.tickerBridge.unregister(this);
        if (this.testClearTimer) {
            clearTimeout(this.testClearTimer);
        }
        for (const sub of this.subs) {
            sub.unsubscribe();
        }
    }

    showTestAlert(playSound: boolean, soundOverride?: string, speakTts?: boolean): void {
        if (this.testClearTimer) {
            clearTimeout(this.testClearTimer);
        }
        this.testMode = true;
        this.loading = false;
        this.flashing = true;
        const testAlerts: NwsSevereAlert[] = [{
            id: 'tlr-weather-test',
            event: 'TEST: Severe Thunderstorm Warning',
            headline: 'This is a sample alert scrolling in the header ticker for your area.',
            severity: 'Severe',
            area: 'Your area',
        }];
        this.alerts = testAlerts;
        this.tickerText = this.buildTickerText(testAlerts);
        this.cdr.markForCheck();

        if (playSound) {
            this.playConfiguredSound(soundOverride);
        }
        if (speakTts) {
            this.weatherAlertTtsService.speakAlerts(testAlerts);
        }

        this.testClearTimer = setTimeout(() => this.clearTestAlert(), TEST_ALERT_DURATION_MS);
    }

    onTickerClick(): void {
        if (!this.flashing) {
            return;
        }
        this.flashing = false;
        this.cdr.markForCheck();
    }

    private clearTestAlert(): void {
        this.testMode = false;
        this.flashing = false;
        this.testClearTimer = null;
        if (this.hasZip) {
            this.refresh();
        } else {
            this.alerts = [];
            this.tickerText = '';
            this.cdr.markForCheck();
        }
    }

    private refresh(): void {
        this.loading = true;
        this.cdr.markForCheck();
        this.nwsService.fetchSevereAlertsForUserArea().pipe(
            catchError(() => of([] as NwsSevereAlert[])),
        ).subscribe((alerts) => {
            this.loading = false;
            this.applyAlerts(alerts);
        });
    }

    private applyAlerts(alerts: NwsSevereAlert[]): void {
        if (this.testMode) {
            return;
        }
        const newIds = alerts.map((a) => a.id).filter(Boolean);
        const hasNew = newIds.some((id) => !this.knownAlertIds.has(id));
        const newAlerts = alerts.filter((a) => a.id && !this.knownAlertIds.has(a.id));
        if (hasNew && this.knownAlertIds.size > 0) {
            this.maybePlaySound();
            this.maybeSpeakAlerts(newAlerts);
        }
        if (hasNew && alerts.length > 0) {
            this.flashing = true;
        }
        this.knownAlertIds = new Set(newIds);

        this.alerts = alerts;
        this.tickerText = this.buildTickerText(alerts);
        if (!alerts.length) {
            this.flashing = false;
        }
        this.cdr.markForCheck();
    }

    private buildTickerText(alerts: NwsSevereAlert[]): string {
        if (!alerts.length) {
            return '';
        }
        return alerts
            .map((a) => `${a.event}: ${a.headline || a.area}`)
            .join('   •   ')
            .toUpperCase();
    }

    private maybePlaySound(): void {
        const enabled = !!this.settingsSnapshot['weatherAlertSoundEnabled'];
        if (!enabled) {
            return;
        }
        this.playConfiguredSound();
    }

    private maybeSpeakAlerts(alerts: NwsSevereAlert[]): void {
        const enabled = !!this.settingsSnapshot['weatherAlertTtsEnabled'];
        if (!enabled || !alerts.length) {
            return;
        }
        this.weatherAlertTtsService.speakAlerts(alerts);
    }

    private playConfiguredSound(soundOverride?: string): void {
        const sound = soundOverride
            || (typeof this.settingsSnapshot['weatherAlertSound'] === 'string'
                ? this.settingsSnapshot['weatherAlertSound']
                : 'alert');
        if (!sound || sound === 'none') {
            return;
        }
        this.alertSoundService.playSound(sound);
    }
}
