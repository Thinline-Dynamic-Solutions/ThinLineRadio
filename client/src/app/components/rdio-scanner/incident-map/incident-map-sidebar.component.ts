/*
 * Copyright (C) 2025 Thinline Dynamic Solutions
 */

import { Component, ChangeDetectionStrategy, ChangeDetectorRef, ElementRef, OnDestroy, ViewChild } from '@angular/core';
import { Observable, Subscription } from 'rxjs';
import { RdioScannerIncidentMapComponent } from './incident-map.component';
import { IncidentMapBridgeService } from './incident-map-bridge.service';

@Component({
    selector: 'rdio-scanner-incident-map-sidebar',
    templateUrl: './incident-map-sidebar.component.html',
    styleUrls: ['./incident-map-sidebar.scss'],
    changeDetection: ChangeDetectionStrategy.OnPush,
    standalone: false
})
export class RdioScannerIncidentMapSidebarComponent implements OnDestroy {
    readonly map$: Observable<RdioScannerIncidentMapComponent | null>;
    showJumpTop = false;
    @ViewChild('incidentList') incidentList?: ElementRef<HTMLElement>;
    private readonly sub: Subscription;

    constructor(
        private bridge: IncidentMapBridgeService,
        private cdr: ChangeDetectorRef,
    ) {
        this.map$ = this.bridge.map$;
        this.sub = this.bridge.refresh$.subscribe(() => this.cdr.markForCheck());
    }

    onIncidentListScroll(event: Event): void {
        const el = event.target as HTMLElement | null;
        const next = (el?.scrollTop || 0) > 48;
        if (next === this.showJumpTop) {
            return;
        }
        this.showJumpTop = next;
        this.cdr.markForCheck();
    }

    scrollIncidentListToTop(): void {
        this.incidentList?.nativeElement.scrollTo({ top: 0, behavior: 'smooth' });
    }

    ngOnDestroy(): void {
        this.sub.unsubscribe();
    }
}
