/*
 * Copyright (C) 2025 Thinline Dynamic Solutions
 */

import {
    AfterViewInit,
    ChangeDetectionStrategy,
    ChangeDetectorRef,
    Component,
    ElementRef,
    Input,
    OnChanges,
    OnDestroy,
} from '@angular/core';
import packageInfo from '../../../../../package.json';

const TILE = 256;
const TILE_CACHE_BUST = packageInfo.version;

interface MapThumbTile {
    src: string;
    left: number;
    top: number;
}

@Component({
    selector: 'rdio-scanner-alert-map-thumb',
    templateUrl: './alert-map-thumb.component.html',
    styleUrls: ['./alert-map-thumb.component.scss'],
    changeDetection: ChangeDetectionStrategy.OnPush,
    standalone: false,
})
export class RdioScannerAlertMapThumbComponent implements AfterViewInit, OnChanges, OnDestroy {
    @Input() lat = 0;
    @Input() lon = 0;
    @Input() zoom = 17;

    tiles: MapThumbTile[] = [];

    private viewWidth = 196;
    private viewHeight = 148;
    private resizeObserver?: ResizeObserver;

    constructor(
        private readonly host: ElementRef<HTMLElement>,
        private readonly cdr: ChangeDetectorRef,
    ) {}

    ngOnChanges(): void {
        this.tiles = this.buildTiles();
    }

    ngAfterViewInit(): void {
        this.resizeObserver = new ResizeObserver((entries) => {
            const box = entries[0]?.contentRect;
            if (!box?.width || !box?.height) {
                return;
            }
            const width = Math.max(64, Math.round(box.width));
            const height = Math.max(64, Math.round(box.height));
            if (width === this.viewWidth && height === this.viewHeight) {
                return;
            }
            this.viewWidth = width;
            this.viewHeight = height;
            this.tiles = this.buildTiles();
            this.cdr.markForCheck();
        });
        this.resizeObserver.observe(this.host.nativeElement);
    }

    ngOnDestroy(): void {
        this.resizeObserver?.disconnect();
    }

    private buildTiles(): MapThumbTile[] {
        const lat = Number(this.lat);
        const lon = Number(this.lon);
        const zoom = Math.max(1, Math.min(19, Math.round(this.zoom) || 17));
        const width = this.viewWidth;
        const height = this.viewHeight;
        if (!Number.isFinite(lat) || !Number.isFinite(lon) || lat === 0 || lon === 0) {
            return [];
        }

        const xf = this.lonToTile(lon, zoom);
        const yf = this.latToTile(lat, zoom);
        const originX = xf * TILE - width / 2;
        const originY = yf * TILE - height / 2;
        const x0 = Math.floor(originX / TILE);
        const y0 = Math.floor(originY / TILE);
        const x1 = Math.floor((originX + width - 1) / TILE);
        const y1 = Math.floor((originY + height - 1) / TILE);
        const n = 2 ** zoom;
        const tiles: MapThumbTile[] = [];

        for (let y = y0; y <= y1; y++) {
            if (y < 0 || y >= n) {
                continue;
            }
            for (let x = x0; x <= x1; x++) {
                const wx = ((x % n) + n) % n;
                tiles.push({
                    src: `/api/map/tiles/voyager/${zoom}/${wx}/${y}.png?v=${TILE_CACHE_BUST}`,
                    left: x * TILE - originX,
                    top: y * TILE - originY,
                });
            }
        }
        return tiles;
    }

    private lonToTile(lon: number, zoom: number): number {
        return ((lon + 180) / 360) * (2 ** zoom);
    }

    private latToTile(lat: number, zoom: number): number {
        const rad = lat * Math.PI / 180;
        return (1 - Math.log(Math.tan(rad) + 1 / Math.cos(rad)) / Math.PI) / 2 * (2 ** zoom);
    }
}
