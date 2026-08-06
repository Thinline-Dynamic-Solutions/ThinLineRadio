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
import { Subscription } from 'rxjs';
import { NwsForecastPeriod, NwsService, NwsWeatherBundle } from './nws.service';

@Component({
    selector: 'rdio-scanner-weather-widget',
    templateUrl: './weather-widget.component.html',
    styleUrls: ['./weather-widget.component.scss'],
    changeDetection: ChangeDetectionStrategy.OnPush,
    standalone: false
})
export class RdioScannerWeatherWidgetComponent implements OnInit, OnDestroy {
    weather: NwsWeatherBundle | null = null;
    loading = false;
    error = '';
    zipInput = '';
    editingZip = false;

    private subs: Subscription[] = [];

    constructor(
        private nwsService: NwsService,
        private cdr: ChangeDetectorRef,
    ) {}

    ngOnInit(): void {
        this.subs.push(
            this.nwsService.getWeather().subscribe((w) => {
                this.weather = w;
                this.cdr.markForCheck();
            }),
            this.nwsService.getLoading().subscribe((v) => {
                this.loading = v;
                this.cdr.markForCheck();
            }),
            this.nwsService.getError().subscribe((v) => {
                this.error = v;
                this.cdr.markForCheck();
            }),
            this.nwsService.getZipCode().subscribe((zip) => {
                this.zipInput = zip;
                this.cdr.markForCheck();
            }),
        );
    }

    ngOnDestroy(): void {
        for (const sub of this.subs) {
            sub.unsubscribe();
        }
    }

    startZipEdit(): void {
        this.editingZip = true;
    }

    saveZip(): void {
        this.editingZip = false;
        this.nwsService.setZipCode(this.zipInput);
    }

    onZipKeydown(event: KeyboardEvent): void {
        if (event.key === 'Enter') {
            this.saveZip();
        }
        if (event.key === 'Escape') {
            this.editingZip = false;
            this.cdr.markForCheck();
        }
    }

    formatHour(timeIso: string): string {
        if (!timeIso) {
            return '';
        }
        return new Date(timeIso).toLocaleTimeString(undefined, { hour: 'numeric' });
    }

    formatDay(period: NwsForecastPeriod): string {
        if (!period.startTime) {
            return period.name;
        }
        const d = new Date(period.startTime);
        return d.toLocaleDateString(undefined, {
            weekday: 'short',
            month: 'numeric',
            day: 'numeric',
        });
    }

    tempLabel(value: number | null, unit = 'F'): string {
        if (value == null) {
            return '—';
        }
        return `${Math.round(value)}°${unit}`;
    }

    precipLabel(value: number | null): string {
        if (value == null || value <= 0) {
            return '';
        }
        return `${value}%`;
    }
}
