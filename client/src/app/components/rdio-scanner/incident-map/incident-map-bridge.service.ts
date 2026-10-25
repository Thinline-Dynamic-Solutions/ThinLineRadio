/*
 * Copyright (C) 2025 Thinline Dynamic Solutions
 */

import { Injectable } from '@angular/core';
import { BehaviorSubject, Subject } from 'rxjs';
import { RdioScannerIncidentMapComponent } from './incident-map.component';

/** Lets the docked scanner-column sidebar find the active map instance. */
@Injectable()
export class IncidentMapBridgeService {
    private readonly mapRef = new BehaviorSubject<RdioScannerIncidentMapComponent | null>(null);
    readonly map$ = this.mapRef.asObservable();
    private readonly refreshRef = new Subject<void>();
    readonly refresh$ = this.refreshRef.asObservable();

    register(map: RdioScannerIncidentMapComponent): void {
        this.mapRef.next(map);
        this.refreshRef.next();
    }

    unregister(map: RdioScannerIncidentMapComponent): void {
        if (this.mapRef.value === map) {
            this.mapRef.next(null);
            this.refreshRef.next();
        }
    }

    notifyChanged(): void {
        this.refreshRef.next();
    }

    getMap(): RdioScannerIncidentMapComponent | null {
        return this.mapRef.value;
    }
}
