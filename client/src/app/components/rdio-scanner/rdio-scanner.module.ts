/*
 * *****************************************************************************
 * Copyright (C) 2019-2024 Chrystian Huot <chrystian@huot.qc.ca>
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

import { FullscreenOverlayContainer, OverlayContainer } from '@angular/cdk/overlay';
import { NgModule } from '@angular/core';
import { AppSharedModule } from '../../shared/shared.module';
import { RdioScannerComponent } from './rdio-scanner.component';
import { RdioScannerService } from './rdio-scanner.service';
import { RdioScannerMainComponent } from './main/main.component';
import { RdioScannerMainLegacyComponent } from './main/main-legacy.component';
import { RdioScannerSupportComponent } from './main/support/support.component';
import { RdioScannerNativeModule } from './native/native.module';
import { RdioScannerSearchComponent } from './search/search.component';
import { RdioScannerSelectComponent } from './select/select.component';
import { SystemsVisibilityDialogComponent } from './select/systems-visibility-dialog.component';
import { RdioScannerUserLoginComponent } from './user-login/user-login.component';
import { RdioScannerUserRegistrationComponent } from './user-registration/user-registration.component';
import { RdioScannerEmailVerificationComponent } from './email-verification/email-verification.component';
import { RdioScannerAuthScreenComponent } from './auth-screen/auth-screen.component';
import { RdioScannerStripeCheckoutComponent } from './stripe-checkout/stripe-checkout.component';
import { RdioScannerSettingsComponent } from './settings/settings.component';
import { RdioScannerAlertsComponent } from './alerts/alerts.component';
import { RdioScannerAlertPreferencesComponent } from './alerts/preferences/preferences.component';
import { SettingsService } from './settings/settings.service';
import { AlertsService } from './alerts/alerts.service';
import { TagColorService } from './tag-color.service';
import { FavoritesService } from './favorites.service';
import { ScanListsService } from './scan-lists.service';
import { RdioScannerSelectLegacyComponent } from './select/select-legacy.component';
import { AlertSoundService } from './alert-sound.service';
import { RdioScannerMobileWebHubComponent } from './mobile-web-hub/mobile-web-hub.component';

@NgModule({
    declarations: [
        RdioScannerComponent,
        RdioScannerMainComponent,
        RdioScannerMainLegacyComponent,
        RdioScannerSearchComponent,
        RdioScannerSelectComponent,
        RdioScannerSelectLegacyComponent,
        SystemsVisibilityDialogComponent,
        RdioScannerSupportComponent,
        RdioScannerUserLoginComponent,
        RdioScannerUserRegistrationComponent,
        RdioScannerEmailVerificationComponent,
        RdioScannerAuthScreenComponent,
        RdioScannerStripeCheckoutComponent,
        RdioScannerSettingsComponent,
        RdioScannerAlertsComponent,
        RdioScannerAlertPreferencesComponent,
        RdioScannerMobileWebHubComponent,
    ],
    exports: [RdioScannerComponent],
    imports: [
        AppSharedModule,
        RdioScannerNativeModule,
    ],
    providers: [
        RdioScannerService,
        SettingsService,
        AlertsService,
        TagColorService,
        FavoritesService,
        ScanListsService,
        AlertSoundService,
        { provide: OverlayContainer, useClass: FullscreenOverlayContainer },
    ],
})
export class RdioScannerModule { }
