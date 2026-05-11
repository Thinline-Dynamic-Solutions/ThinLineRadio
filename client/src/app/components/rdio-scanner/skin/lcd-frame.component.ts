/*
 * ThinLine Radio — LCD frame wrapper
 *
 * Web parity for `ScannerLcdFrame` from the mobile app:
 *   - Outer bezel (border + shadow + glow)
 *   - LCD-face gradient interior
 *   - Optional CRT scanlines overlay
 *
 * Pair with <rdio-scanner-chassis> as the screen background.
 */
import { ChangeDetectionStrategy, Component, Input } from '@angular/core';

@Component({
    selector: 'rdio-scanner-lcd-frame',
    templateUrl: './lcd-frame.component.html',
    styleUrls: ['./lcd-frame.component.scss'],
    changeDetection: ChangeDetectionStrategy.OnPush,
})
export class RdioScannerLcdFrameComponent {
    /** Toggle the CRT scanline overlay on top of the LCD face. */
    @Input() showScanlines = true;

    /** Optional content padding inside the bezel (CSS length). */
    @Input() padding = '8px';
}
