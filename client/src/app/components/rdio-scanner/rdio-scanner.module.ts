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
import { RdioScannerConsoleComponent } from './console/console.component';
import { RdioScannerIncidentMapComponent } from './incident-map/incident-map.component';
import { RdioScannerIncidentMapSidebarComponent } from './incident-map/incident-map-sidebar.component';
import { IncidentsService } from './incident-map/incidents.service';
import { IncidentMapBridgeService } from './incident-map/incident-map-bridge.service';
import { RdioScannerSupportComponent } from './main/support/support.component';
import { RdioScannerNativeModule } from './native/native.module';
import { RdioScannerSearchComponent } from './search/search.component';
import { RdioScannerSelectComponent } from './select/select.component';
import { SystemsVisibilityDialogComponent } from './select/systems-visibility-dialog.component';
import { ScanListEditDialogComponent } from './select/scan-list-edit-dialog.component';
import { RdioScannerUserLoginComponent } from './user-login/user-login.component';
import { RdioScannerUserRegistrationComponent } from './user-registration/user-registration.component';
import { RdioScannerEmailVerificationComponent } from './email-verification/email-verification.component';
import { RdioScannerPostVerifyPlanComponent } from './post-verify-plan/post-verify-plan.component';
import { RdioScannerPostVerifyWelcomeComponent } from './post-verify-welcome/post-verify-welcome.component';
import { RdioScannerAuthScreenComponent } from './auth-screen/auth-screen.component';
import { RdioScannerStripeCheckoutComponent } from './stripe-checkout/stripe-checkout.component';
import { RdioScannerSettingsComponent } from './settings/settings.component';
import { RdioScannerAlertsComponent } from './alerts/alerts.component';
import { RdioScannerAlertPreferencesComponent } from './alerts/preferences/preferences.component';
import { KeywordListInfoDialogComponent } from './alerts/preferences/keyword-list-info-dialog.component';
import { SettingsService } from './settings/settings.service';
import { AlertsService } from './alerts/alerts.service';
import { TagColorService } from './tag-color.service';
import { FavoritesService } from './favorites.service';
import { ScanListsService } from './scan-lists.service';
import { AlertSoundService } from './alert-sound.service';
import { RdioScannerAdminService } from './admin/admin.service';
import { TranscriptReviewService } from './transcript-review/transcript-review.service';
import { RdioScannerTranscriptTrainingTipsComponent } from './transcript-review/transcript-training-tips.component';
import { RdioScannerMobileWebHubComponent } from './mobile-web-hub/mobile-web-hub.component';
import { RdioScannerChassisComponent } from './skin/chassis.component';
import { RdioScannerLcdFrameComponent } from './skin/lcd-frame.component';
import { RdioScannerLcdBottomNavComponent } from './skin/lcd-bottom-nav.component';
import { RdioScannerWeatherWidgetComponent } from './weather/weather-widget.component';
import { RdioScannerWeatherAlertTickerComponent } from './weather/weather-alert-ticker.component';
import { WeatherAlertTickerBridgeService } from './weather/weather-alert-ticker-bridge.service';
import { NwsService } from './weather/nws.service';
import { WeatherAlertTtsService } from './weather/weather-alert-tts.service';

@NgModule({
    declarations: [
        RdioScannerComponent,
        RdioScannerConsoleComponent,
        RdioScannerIncidentMapComponent,
        RdioScannerIncidentMapSidebarComponent,
        RdioScannerSearchComponent,
        RdioScannerSelectComponent,
        SystemsVisibilityDialogComponent,
        ScanListEditDialogComponent,
        RdioScannerSupportComponent,
        RdioScannerUserLoginComponent,
        RdioScannerUserRegistrationComponent,
        RdioScannerEmailVerificationComponent,
        RdioScannerPostVerifyPlanComponent,
        RdioScannerPostVerifyWelcomeComponent,
        RdioScannerAuthScreenComponent,
        RdioScannerStripeCheckoutComponent,
        RdioScannerSettingsComponent,
        RdioScannerAlertsComponent,
        RdioScannerTranscriptTrainingTipsComponent,
        RdioScannerAlertPreferencesComponent,
        KeywordListInfoDialogComponent,
        RdioScannerMobileWebHubComponent,
        RdioScannerChassisComponent,
        RdioScannerLcdFrameComponent,
        RdioScannerLcdBottomNavComponent,
        RdioScannerWeatherWidgetComponent,
        RdioScannerWeatherAlertTickerComponent,
    ],
    exports: [
        RdioScannerComponent,
        RdioScannerChassisComponent,
        RdioScannerLcdFrameComponent,
        RdioScannerLcdBottomNavComponent,
    ],
    imports: [
        AppSharedModule,
        RdioScannerNativeModule,
    ],
    providers: [
        RdioScannerService,
        SettingsService,
        AlertsService,
        IncidentsService,
        IncidentMapBridgeService,
        WeatherAlertTickerBridgeService,
        TagColorService,
        FavoritesService,
        ScanListsService,
        AlertSoundService,
        RdioScannerAdminService,
        TranscriptReviewService,
        NwsService,
        WeatherAlertTtsService,
        { provide: OverlayContainer, useClass: FullscreenOverlayContainer },
    ],
})
export class RdioScannerModule { }
