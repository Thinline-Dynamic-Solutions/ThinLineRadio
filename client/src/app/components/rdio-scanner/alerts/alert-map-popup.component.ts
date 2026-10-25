/*
 * Copyright (C) 2025 Thinline Dynamic Solutions
 */

import {
    AfterViewInit,
    Component,
    ElementRef,
    EventEmitter,
    HostListener,
    Input,
    NgZone,
    OnDestroy,
    Output,
    ViewChild,
} from '@angular/core';
import * as L from 'leaflet';
import packageInfo from '../../../../../package.json';

const TILE_CACHE_BUST = packageInfo.version;
const START_ZOOM = 17;

@Component({
    selector: 'rdio-scanner-alert-map-popup',
    templateUrl: './alert-map-popup.component.html',
    styleUrls: ['./alert-map-popup.component.scss'],
    standalone: false,
})
export class RdioScannerAlertMapPopupComponent implements AfterViewInit, OnDestroy {
    @Input() lat = 0;
    @Input() lon = 0;
    @Input() title = 'Incident location';
    @Input() subtitle = '';
    @Output() closed = new EventEmitter<void>();

    @ViewChild('mapCanvas', { static: true }) mapCanvas?: ElementRef<HTMLDivElement>;

    private map?: L.Map;
    private resizeTimer?: ReturnType<typeof setTimeout>;

    constructor(private readonly ngZone: NgZone) {}

    ngAfterViewInit(): void {
        // Leaflet timers must stay outside Angular. A zoom otherwise change-detects
        // the whole alerts board (every card and map thumb) until the tab dies.
        this.ngZone.runOutsideAngular(() => this.mountMap());
    }

    private mountMap(): void {
        const el = this.mapCanvas?.nativeElement;
        if (!el || this.map) {
            return;
        }
        const lat = Number(this.lat);
        const lon = Number(this.lon);
        const style = this.storedMapStyle();
        const maxZoom = style === 'dark' ? 16 : 18;
        L.DomEvent.disableScrollPropagation(el);
        L.DomEvent.disableClickPropagation(el);
        this.map = L.map(el, {
            center: [lat, lon],
            zoom: START_ZOOM,
            minZoom: 11,
            maxZoom,
            scrollWheelZoom: true,
            wheelPxPerZoomLevel: 160,
            zoomSnap: 1,
            zoomDelta: 1,
            zoomAnimation: false,
            fadeAnimation: false,
            markerZoomAnimation: false,
            inertia: false,
            bounceAtZoomLimits: false,
        });
        L.tileLayer(`/api/map/tiles/${style}/{z}/{x}/{y}.png?v=${TILE_CACHE_BUST}`, {
            maxZoom,
            maxNativeZoom: maxZoom,
            updateWhenZooming: false,
            updateWhenIdle: true,
            keepBuffer: 1,
            updateInterval: 250,
            crossOrigin: true,
            attribution: style === 'voyager'
                ? '&copy; OpenStreetMap contributors'
                : 'Tiles &copy; Esri',
        }).addTo(this.map);
        L.marker([lat, lon], {
            icon: L.divIcon({
                className: 'alert-map-pop-pin',
                html: '<span class="alert-map-pop-pin__mark"></span>',
                iconSize: [22, 28],
                iconAnchor: [11, 26],
            }),
            keyboard: false,
        }).addTo(this.map);
        this.resizeTimer = setTimeout(() => this.map?.invalidateSize(), 80);
    }

    ngOnDestroy(): void {
        if (this.resizeTimer) {
            clearTimeout(this.resizeTimer);
        }
        this.map?.remove();
        this.map = undefined;
    }

    @HostListener('document:keydown.escape')
    onEscape(): void {
        this.closed.emit();
    }

    close(): void {
        this.closed.emit();
    }

    private storedMapStyle(): 'voyager' | 'dark' | 'satellite' {
        try {
            const stored = localStorage.getItem('tlr-incident-map-style');
            if (stored === 'voyager' || stored === 'dark' || stored === 'satellite') {
                return stored;
            }
        } catch {
            // ignore
        }
        return 'voyager';
    }
}
