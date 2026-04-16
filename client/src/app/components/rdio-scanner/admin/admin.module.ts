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

import { HttpClientModule } from '@angular/common/http';
import { NgModule } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { ErrorStateMatcher, ShowOnDirtyErrorStateMatcher } from '@angular/material/core';
import { MAT_SELECT_CONFIG } from '@angular/material/select';
import { MatProgressSpinnerModule } from '@angular/material/progress-spinner';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { MatChipsModule } from '@angular/material/chips';
import { MatPaginatorModule } from '@angular/material/paginator';
import { MatSnackBarModule } from '@angular/material/snack-bar';
import { AppSharedModule } from '../../../shared/shared.module';
import { RdioScannerAdminComponent } from './admin.component';
import { RdioScannerAdminService } from './admin.service';
import { RdioScannerAdminConfigComponent } from './config/config.component';
import { RdioScannerAdminApikeysComponent } from './config/apikeys/apikeys.component';
import { RdioScannerAdminDirwatchComponent } from './config/dirwatch/dirwatch.component';
import { RdioScannerAdminDownstreamsComponent } from './config/downstreams/downstreams.component';
import { RdioScannerAdminGroupsComponent } from './config/groups/groups.component';
import { RdioScannerAdminOptionsComponent } from './config/options/options.component';
import { RdioScannerAdminSiteComponent } from './config/systems/site/site.component';
import { RdioScannerAdminSystemsSelectComponent } from './config/systems/select/select.component';
import { RdioScannerAdminSystemComponent } from './config/systems/system/system.component';
import { RdioScannerAdminSystemsComponent } from './config/systems/systems.component';
import { RdioScannerAdminTalkgroupComponent } from './config/systems/talkgroup/talkgroup.component';
import { RdioScannerAdminUnitComponent } from './config/systems/unit/unit.component';
import { RdioScannerAdminTagsComponent } from './config/tags/tags.component';
import { RdioScannerAdminUserRegistrationComponent } from './config/user-registration/user-registration.component';
import { RdioScannerAdminUsersComponent } from './config/users/users.component';
import { InviteUserDialogComponent, InvitationResultsDialogComponent, CreateUserDialogComponent, ResetPasswordDialogComponent } from './config/users/invite-user-dialog.component';
import { TransferUserDialogComponent } from './config/users/transfer-user-dialog.component';
import { RequestAPIKeyDialogComponent } from './config/options/request-api-key-dialog.component';
import { RecoverAPIKeyDialogComponent } from './config/options/recover-api-key-dialog.component';
import { RdioScannerAdminUserGroupsComponent } from './config/user-groups/user-groups.component';
import { RdioScannerAdminKeywordListsComponent } from './config/keyword-lists/keyword-lists.component';
import { RdioScannerAdminTranscriptParserComponent } from './config/transcript-parser/transcript-parser.component';
import { RdioScannerAdminLoginComponent } from './login/login.component';
import { AlertsService } from '../alerts/alerts.service';
import { RdioScannerAdminLogsComponent } from './logs/logs.component';
import { RdioScannerAdminTodosComponent } from './todos/todos.component';
import { RdioScannerAdminToolsComponent } from './tools/tools.component';
import { RdioScannerAdminImportExportConfigComponent } from './tools/import-export-config/import-export-config.component';
import { RdioScannerAdminImportTalkgroupsComponent } from './tools/import-talkgroups/import-talkgroups.component';
import { RdioScannerAdminImportUnitsComponent } from './tools/import-units/import-units.component';
import { RdioScannerAdminPasswordComponent } from './tools/password/password.component';
import { RdioScannerAdminRadioReferenceImportComponent } from './tools/radio-reference-import/radio-reference-import.component';
import { RdioScannerAdminConfigSyncComponent } from './tools/config-sync/config-sync.component';
import { RdioScannerAdminStripeSyncComponent } from './tools/stripe-sync/stripe-sync.component';
import { RdioScannerAdminPurgeDataComponent } from './tools/purge-data/purge-data.component';
import { RdioScannerAdminSystemHealthComponent } from './system-health/system-health.component';

@NgModule({
    declarations: [
        RdioScannerAdminComponent,
        RdioScannerAdminConfigComponent,
        RdioScannerAdminApikeysComponent,
        RdioScannerAdminDirwatchComponent,
        RdioScannerAdminDownstreamsComponent,
        RdioScannerAdminGroupsComponent,
        RdioScannerAdminImportExportConfigComponent,
        RdioScannerAdminImportTalkgroupsComponent,
        RdioScannerAdminImportUnitsComponent,
        RdioScannerAdminLoginComponent,
        RdioScannerAdminLogsComponent,
        RdioScannerAdminOptionsComponent,
        RdioScannerAdminPasswordComponent,
        RdioScannerAdminSiteComponent,
        RdioScannerAdminSystemComponent,
        RdioScannerAdminSystemsComponent,
        RdioScannerAdminSystemsSelectComponent,
        RdioScannerAdminTagsComponent,
        RdioScannerAdminTalkgroupComponent,
        RdioScannerAdminUserRegistrationComponent,
        RdioScannerAdminUsersComponent,
        InviteUserDialogComponent,
        InvitationResultsDialogComponent,
        CreateUserDialogComponent,
        ResetPasswordDialogComponent,
        TransferUserDialogComponent,
        RequestAPIKeyDialogComponent,
        RecoverAPIKeyDialogComponent,
        RdioScannerAdminUserGroupsComponent,
        RdioScannerAdminTodosComponent,
        RdioScannerAdminToolsComponent,
        RdioScannerAdminUnitComponent,
        RdioScannerAdminRadioReferenceImportComponent,
        RdioScannerAdminKeywordListsComponent,
        RdioScannerAdminTranscriptParserComponent,
        RdioScannerAdminConfigSyncComponent,
        RdioScannerAdminStripeSyncComponent,
        RdioScannerAdminPurgeDataComponent,
        RdioScannerAdminSystemHealthComponent,
    ],
    entryComponents: [RdioScannerAdminSystemsSelectComponent],
    exports: [RdioScannerAdminComponent],
    imports: [AppSharedModule, HttpClientModule, FormsModule, MatProgressSpinnerModule, MatProgressBarModule, MatChipsModule, MatPaginatorModule, MatSnackBarModule],
    providers: [
        RdioScannerAdminService,
        AlertsService,
        // Only show mat-form-field error styling after the user changes a control,
        // not when code calls markAsTouched() (avoids spurious red icons on load).
        { provide: ErrorStateMatcher, useClass: ShowOnDirtyErrorStateMatcher },
        // Apply dark-theme overlay styles to every mat-select in the admin module.
        // The CDK overlay is rendered outside .admin-dark-theme, so panel colours
        // must be driven by a global panelClass rather than the scoped theme.
        { provide: MAT_SELECT_CONFIG, useValue: { panelClass: 'admin-select-panel' } },
    ],
})
export class RdioScannerAdminModule { }
