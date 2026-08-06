/*
 * Copyright (C) 2025 Thinline Dynamic Solutions
 */

import { Component, ChangeDetectionStrategy } from '@angular/core';
import { Observable } from 'rxjs';
import { RdioScannerIncidentMapComponent } from './incident-map.component';
import { IncidentMapBridgeService } from './incident-map-bridge.service';

@Component({
    selector: 'rdio-scanner-incident-map-sidebar',
    templateUrl: './incident-map-sidebar.component.html',
    styleUrls: ['./incident-map-sidebar.scss'],
    changeDetection: ChangeDetectionStrategy.Eager,
    standalone: false
})
export class RdioScannerIncidentMapSidebarComponent {
    readonly map$: Observable<RdioScannerIncidentMapComponent | null>;

    constructor(private bridge: IncidentMapBridgeService) {
        this.map$ = this.bridge.map$;
    }
}
