/*
 * ThinLine Radio — Chassis (full-screen background)
 *
 * Web parity for the mobile `ScannerSkin.chassisDecoration`. Drop this as
 * the outer container of any screen so the body sits on the same dark
 * scanner-shell gradient the mobile app uses.
 */
import { ChangeDetectionStrategy, Component } from '@angular/core';

@Component({
    selector: 'rdio-scanner-chassis',
    template: `<div class="chassis"><ng-content></ng-content></div>`,
    styleUrls: ['./chassis.component.scss'],
    changeDetection: ChangeDetectionStrategy.OnPush,
})
export class RdioScannerChassisComponent {}
