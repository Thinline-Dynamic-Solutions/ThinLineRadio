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

import { Component, Input, OnInit, OnDestroy, OnChanges, SimpleChanges, ChangeDetectorRef } from '@angular/core';
import { FormGroup, Validators } from '@angular/forms';
import { Subscription } from 'rxjs';
import { MatSnackBar } from '@angular/material/snack-bar';
import { MatDialog } from '@angular/material/dialog';
import { HttpClient, HttpHeaders } from '@angular/common/http';
import { RequestAPIKeyDialogComponent } from './request-api-key-dialog.component';
import { RecoverAPIKeyDialogComponent } from './recover-api-key-dialog.component';
import { LocationDataService } from 'src/app/services/location-data.service';

@Component({
    selector: 'rdio-scanner-admin-options',
    templateUrl: './options.component.html',
})
export class RdioScannerAdminOptionsComponent implements OnInit, OnDestroy, OnChanges {
    @Input() form: FormGroup | undefined;
    private radioReferenceSubscription?: Subscription;
    private initialLoadComplete = false;
    public isEditingRadioReference = false;
    private originalRadioReferenceUsername = '';
    private originalRadioReferencePassword = '';
    faviconUrl: string = '';
    window = window;
    
    // Central Management Integration
    centralConnectionStatus: 'success' | 'error' | null = null;
    centralConnectionMessage: string = '';

    constructor(
        private snackBar: MatSnackBar,
        private dialog: MatDialog,
        private locationService: LocationDataService,
        private http: HttpClient,
        private cdr: ChangeDetectorRef
    ) {}

    get isRadioReferenceLoggedIn(): boolean {
        return this.hasStoredRadioReferenceCredentials();
    }

    get shouldShowLoginForm(): boolean {
        return this.isEditingRadioReference || !this.isRadioReferenceLoggedIn;
    }

    ngOnInit(): void {
        this.setupRadioReferenceValidation();
        this.setInitialRadioReferenceValidation();
        this.storeOriginalCredentials();
        this.setupRelayServerFormListeners();
        // Ensure hardcoded relay server URL is set
        this.setHardcodedRelayServerURL();
        // Initialize favicon URL
        this.updateFaviconUrl();
        // Mark initial load as complete after a brief delay to allow form to populate
        setTimeout(() => {
            this.initialLoadComplete = true;
        }, 100);
    }

    ngOnDestroy(): void {
        this.radioReferenceSubscription?.unsubscribe();
    }

    ngOnChanges(changes: SimpleChanges): void {
        if (changes['form'] && this.form) {
            this.setupRadioReferenceValidation();
            this.setInitialRadioReferenceValidation();
            this.storeOriginalCredentials();
            this.setupRelayServerFormListeners();
            this.setHardcodedRelayServerURL();
            this.isEditingRadioReference = false;
        }
    }

    private setupRadioReferenceValidation(): void {
        if (!this.form) return;

        const radioReferenceEnabledControl = this.form.get('radioReferenceEnabled');
        const usernameControl = this.form.get('radioReferenceUsername');
        const passwordControl = this.form.get('radioReferencePassword');

        if (radioReferenceEnabledControl && usernameControl && passwordControl) {
            // Listen to enabled toggle changes
            this.radioReferenceSubscription = radioReferenceEnabledControl.valueChanges.subscribe(enabled => {
                if (enabled) {
                    usernameControl.setValidators([Validators.required]);
                    passwordControl.setValidators([Validators.required]);
                } else {
                    usernameControl.clearValidators();
                    passwordControl.clearValidators();
                }
                
                usernameControl.updateValueAndValidity();
                passwordControl.updateValueAndValidity();
                
                // Force form to detect changes
                if (this.form) {
                    this.form.markAsDirty();
                    this.form.updateValueAndValidity();
                }
            });

            // Listen to username changes (only after initial load to avoid marking form dirty on auto-populate)
            usernameControl.valueChanges.subscribe(() => {
                if (this.initialLoadComplete) {
                    if (this.form) {
                        this.form.markAsDirty();
                    }
                }
            });

            // Listen to password changes (only after initial load to avoid marking form dirty on auto-populate)
            passwordControl.valueChanges.subscribe(() => {
                if (this.initialLoadComplete) {
                    if (this.form) {
                        this.form.markAsDirty();
                    }
                }
            });
        }
    }

    private setInitialRadioReferenceValidation(): void {
        if (!this.form) return;

        const radioReferenceEnabledControl = this.form.get('radioReferenceEnabled');
        const usernameControl = this.form.get('radioReferenceUsername');
        const passwordControl = this.form.get('radioReferencePassword');

        if (radioReferenceEnabledControl && usernameControl && passwordControl) {
            const enabled = radioReferenceEnabledControl.value;
            if (enabled) {
                usernameControl.setValidators([Validators.required]);
                passwordControl.setValidators([Validators.required]);
            } else {
                usernameControl.clearValidators();
                passwordControl.clearValidators();
            }
            
            usernameControl.updateValueAndValidity();
            passwordControl.updateValueAndValidity();
        }
    }

    private storeOriginalCredentials(): void {
        if (!this.form) return;
        
        // Store the current values as original values
        this.originalRadioReferenceUsername = this.form.get('radioReferenceUsername')?.value || '';
        this.originalRadioReferencePassword = this.form.get('radioReferencePassword')?.value || '';
    }

    editRadioReferenceLogin(): void {
        if (!this.form) return;
        
        // Store current values as original before editing
        this.storeOriginalCredentials();
        
        // Enter edit mode
        this.isEditingRadioReference = true;
        
        // Keep the username but clear the password for editing
        this.form.get('radioReferencePassword')?.setValue('');
        this.form.markAsDirty();
    }

    cancelEditRadioReference(): void {
        if (!this.form) return;
        
        // Restore the original username and password values
        this.form.get('radioReferenceUsername')?.setValue(this.originalRadioReferenceUsername);
        this.form.get('radioReferencePassword')?.setValue(this.originalRadioReferencePassword);
        
        // Exit edit mode
        this.isEditingRadioReference = false;
        
        // Mark form as pristine since we've restored original values
        this.form.markAsPristine();
    }

    removeRadioReferenceAccount(): void {
        if (!this.form) return;
        
        // Exit edit mode if we were editing
        this.isEditingRadioReference = false;
        
        // Clear credentials and disable Radio Reference
        this.form.get('radioReferenceEnabled')?.setValue(false);
        this.form.get('radioReferenceUsername')?.setValue('');
        this.form.get('radioReferencePassword')?.setValue('');
        this.originalRadioReferenceUsername = '';
        this.originalRadioReferencePassword = '';
        this.form.markAsDirty();
    }

    private hasStoredRadioReferenceCredentials(): boolean {
        return !!(this.originalRadioReferenceUsername && this.originalRadioReferencePassword);
    }

    private setHardcodedRelayServerURL(): void {
        if (!this.form) return;
        
        const relayServerURLControl = this.form.get('relayServerURL');
        if (relayServerURLControl) {
            relayServerURLControl.setValue('https://tlradioserver.thinlineds.com', { emitEvent: false });
        }
    }

    private setupRelayServerFormListeners(): void {
        if (!this.form) return;

        const relayServerURLControl = this.form.get('relayServerURL');
        const relayServerAPIKeyControl = this.form.get('relayServerAPIKey');

        // Don't listen to relayServerURL changes since it's hardcoded
        // if (relayServerURLControl) {
        //     relayServerURLControl.valueChanges.subscribe(() => {
        //         if (this.initialLoadComplete && this.form) {
        //             this.form.markAsDirty();
        //         }
        //     });
        // }

        if (relayServerAPIKeyControl) {
            relayServerAPIKeyControl.valueChanges.subscribe(() => {
                if (this.initialLoadComplete && this.form) {
                    this.form.markAsDirty();
                }
            });
        }
    }

    hasRelayAPIKey(): boolean {
        if (!this.form) return false;
        const apiKey = this.form.get('relayServerAPIKey')?.value;
        return apiKey && apiKey.trim().length > 0;
    }

    requestRelayAPIKey() {
        this.editRelayAPIKey();
    }

    editRelayAPIKey() {
        if (!this.form) return;

        // Use hardcoded relay server URL
        const relayServerURL = 'https://tlradioserver.thinlineds.com';
        const existingAPIKey = this.form.get('relayServerAPIKey')?.value;
        
        // Ensure the form control has the hardcoded value
        const relayServerURLControl = this.form.get('relayServerURL');
        if (relayServerURLControl) {
            relayServerURLControl.setValue(relayServerURL, { emitEvent: false });
        }

        const dialogRef = this.dialog.open(RequestAPIKeyDialogComponent, {
            width: '600px',
            data: { 
                relayServerURL: relayServerURL,
                existingAPIKey: existingAPIKey || null
            }
        });

        dialogRef.afterClosed().subscribe((apiKey: string | null) => {
            if (apiKey && this.form) {
                this.form.get('relayServerAPIKey')?.setValue(apiKey);
                this.form.markAsDirty();
                const message = existingAPIKey 
                    ? 'API key details updated! Make sure to save your configuration.' 
                    : 'API key received! Make sure to save your configuration.';
                this.snackBar.open(message, 'Close', { duration: 5000 });
            }
        });
    }

    recoverRelayAPIKey() {
        if (!this.form) return;

        // Use hardcoded relay server URL
        const relayServerURL = 'https://tlradioserver.thinlineds.com';

        const dialogRef = this.dialog.open(RecoverAPIKeyDialogComponent, {
            width: '600px',
            data: { relayServerURL: relayServerURL }
        });

        dialogRef.afterClosed().subscribe((apiKey: string | null) => {
            if (apiKey && this.form) {
                this.form.get('relayServerAPIKey')?.setValue(apiKey);
                this.form.markAsDirty();
                this.snackBar.open('API key recovered! Make sure to save your configuration.', 'Close', { duration: 5000 });
            }
        });
    }

    // Favicon upload methods
    hasFavicon(): boolean {
        return !!(this.form?.get('faviconFilename')?.value);
    }

    getFaviconPreview(): string {
        if (this.faviconUrl) {
            return this.faviconUrl;
        }
        return `${window.location.origin}/favicon?t=${Date.now()}`;
    }

    onFaviconSelected(event: Event): void {
        const input = event.target as HTMLInputElement;
        if (input.files && input.files.length > 0) {
            const file = input.files[0];
            
            // Validate file size (max 2MB)
            if (file.size > 2 * 1024 * 1024) {
                alert('File is too large. Maximum size is 2MB.');
                return;
            }

            // Validate file type
            if (!file.type.startsWith('image/')) {
                alert('Please select an image file.');
                return;
            }

            this.uploadFavicon(file);
        }
    }

    private uploadFavicon(file: File): void {
        const formData = new FormData();
        formData.append('favicon', file);

        // Get auth token from session storage (admin service sends token without "Bearer" prefix)
        const token = sessionStorage.getItem('rdio-scanner-admin-token');
        if (!token) {
            alert('Not authenticated. Please log in again.');
            return;
        }

        // HttpHeaders is immutable, so create with headers already set
        const headers = new HttpHeaders({
            'Authorization': token
        });

        console.log('Uploading favicon with token:', token ? 'Token present (' + token.substring(0, 20) + '...)' : 'No token');

        this.http.post(`${window.location.origin}/api/admin/favicon`, formData, { headers })
            .subscribe({
                next: (response: any) => {
                    if (response.success && response.filename) {
                        this.form?.get('faviconFilename')?.setValue(response.filename, { emitEvent: false });
                        this.faviconUrl = `${window.location.origin}/favicon?t=${Date.now()}`;
                        this.cdr.detectChanges();
                        this.snackBar.open('Favicon uploaded successfully', 'Close', { duration: 3000 });
                        // Mark form as dirty so user knows to save
                        this.form?.markAsDirty();
                    } else {
                        alert('Failed to upload favicon: ' + (response.error || 'Unknown error'));
                    }
                },
                error: (error) => {
                    console.error('Favicon upload error:', error);
                    let errorMsg = 'Failed to upload favicon.';
                    if (error.status === 0) {
                        errorMsg += ' The file may be too large or the connection timed out.';
                    } else if (error.status === 413) {
                        errorMsg += ' The file is too large.';
                    } else if (error.error && error.error.error) {
                        errorMsg += ' ' + error.error.error;
                    }
                    alert(errorMsg);
                }
            });
    }

    removeFavicon(): void {
        // Get auth token from session storage (admin service sends token without "Bearer" prefix)
        const token = sessionStorage.getItem('rdio-scanner-admin-token');
        if (!token) {
            alert('Not authenticated. Please log in again.');
            return;
        }

        // HttpHeaders is immutable, so create with headers already set
        const headers = new HttpHeaders({
            'Authorization': token
        });

        this.http.delete(`${window.location.origin}/api/admin/favicon/delete`, { headers })
            .subscribe({
                next: (response: any) => {
                    if (response.success) {
                        this.form?.get('faviconFilename')?.setValue('', { emitEvent: false });
                        this.faviconUrl = '';
                        this.cdr.detectChanges();
                        this.snackBar.open('Favicon removed successfully', 'Close', { duration: 3000 });
                        // Mark form as dirty so user knows to save
                        this.form?.markAsDirty();
                    } else {
                        alert('Failed to remove favicon: ' + (response.error || 'Unknown error'));
                    }
                },
                error: (error) => {
                    console.error('Favicon removal error:', error);
                    alert('Failed to remove favicon: ' + (error.error?.error || error.message || 'Unknown error'));
                }
            });
    }

    private updateFaviconUrl(): void {
        if (this.hasFavicon()) {
            this.faviconUrl = `${window.location.origin}/favicon?t=${Date.now()}`;
        } else {
            this.faviconUrl = '';
        }
    }

    testCentralConnection(): void {
        const url = this.form?.get('centralManagementURL')?.value;
        const apiKey = this.form?.get('centralManagementAPIKey')?.value;

        if (!url || !apiKey) {
            this.snackBar.open('Please enter both URL and API key', 'Close', { duration: 3000 });
            return;
        }

        // Test connection to central system
        const testUrl = `${url}/api/webhook/central-test?api_key=${encodeURIComponent(apiKey)}`;
        const headers = new HttpHeaders({
            'X-API-Key': apiKey
        });

        this.centralConnectionStatus = null;
        this.centralConnectionMessage = 'Testing connection...';

        this.http.get(testUrl, { headers }).subscribe({
            next: (response: any) => {
                this.centralConnectionStatus = 'success';
                this.centralConnectionMessage = `Connected successfully! Server: ${response.server || 'Unknown'}`;
                this.snackBar.open('Connection test successful', 'Close', { duration: 3000 });
            },
            error: (error) => {
                this.centralConnectionStatus = 'error';
                this.centralConnectionMessage = `Connection failed: ${error.statusText || 'Unknown error'}`;
                this.snackBar.open('Connection test failed', 'Close', { duration: 5000 });
            }
        });
    }
}
