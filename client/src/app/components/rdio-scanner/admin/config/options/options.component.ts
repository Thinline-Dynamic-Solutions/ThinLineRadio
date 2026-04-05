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

import { Component, Input, Output, EventEmitter, OnInit, OnDestroy, OnChanges, SimpleChanges, AfterViewInit, ChangeDetectorRef, ChangeDetectionStrategy } from '@angular/core';
import { FormGroup, FormArray, Validators } from '@angular/forms';
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
export class RdioScannerAdminOptionsComponent implements OnInit, AfterViewInit, OnDestroy, OnChanges {
    @Input() form: FormGroup | undefined;
    @Input() systemsForm: FormArray | undefined;
    @Output() saveRequested = new EventEmitter<void>();
    private radioReferenceSubscription?: Subscription;
    private initialLoadComplete = false;
    public isEditingRadioReference = false;
    panelsReady = false;
    private originalRadioReferenceUsername = '';
    private originalRadioReferencePassword = '';
    faviconUrl: string = '';
    window = window;
    
    // Expansion panel state - all collapsed by default
    generalExpanded = false;
    brandingExpanded = false;
    transcriptionExpanded = false;
    alertsExpanded = false;
    emailExpanded = false;
    notificationsExpanded = false;
    userRegistrationExpanded = false;
    stripeExpanded = false;
    integrationsExpanded = false;
    securityExpanded = false;
    
    // Central Management Integration
    centralConnectionStatus: 'success' | 'error' | null = null;
    centralConnectionMessage: string = '';
    showExternalAPIKey: boolean = false;

    constructor(
        private snackBar: MatSnackBar,
        private dialog: MatDialog,
        private locationService: LocationDataService,
        private http: HttpClient,
        private cdr: ChangeDetectorRef
    ) {}

    asFormGroup(ctrl: any): FormGroup {
        return ctrl as FormGroup;
    }

    /** Programmatically open a specific expansion panel (called by global search). */
    openPanel(panelName: string): void {
        const key = panelName as keyof this;
        if (key in this) {
            (this as any)[key] = true;
            this.cdr.markForCheck();
            setTimeout(() => {
                const el = document.getElementById('opt-panel-' + panelName);
                if (el) el.scrollIntoView({ behavior: 'smooth', block: 'start' });
            }, 180);
        }
    }

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
        this.setupRateLimitingToggle();
        this.setupAudioEncryptionToggle();
        this.setHardcodedRelayServerURL();
        this.updateFaviconUrl();
        this.updateEmailLogoUrl();
        setTimeout(() => {
            this.initialLoadComplete = true;
        }, 100);
    }

    ngAfterViewInit(): void {
        setTimeout(() => {
            this.panelsReady = true;
            this.cdr.detectChanges();
        }, 80);
    }

    ngOnDestroy(): void {
        this.radioReferenceSubscription?.unsubscribe();
    }

    ngOnChanges(changes: SimpleChanges): void {
        if (changes['form'] && this.form) {
            // Collapse all panels explicitly before hiding so they are in the right state when re-shown
            this.generalExpanded = false;
            this.brandingExpanded = false;
            this.transcriptionExpanded = false;
            this.alertsExpanded = false;
            this.notificationsExpanded = false;
            this.userRegistrationExpanded = false;
            this.stripeExpanded = false;
            this.integrationsExpanded = false;
            this.securityExpanded = false;

            this.panelsReady = false;
            this.cdr.detectChanges(); // Force the hide to apply immediately

            this.setupRadioReferenceValidation();
            this.setInitialRadioReferenceValidation();
            this.storeOriginalCredentials();
            this.setupRelayServerFormListeners();
            this.setupRateLimitingToggle();
            this.setupAudioEncryptionToggle();
            this.setHardcodedRelayServerURL();
            this.isEditingRadioReference = false;
            this.updateEmailLogoUrl();

            setTimeout(() => {
                this.panelsReady = true;
                this.cdr.detectChanges();
            }, 80);
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

    private setupRateLimitingToggle(): void {
        if (!this.form) return;

        const toggleControl = this.form.get('rateLimitingEnabled');
        const maxControl = this.form.get('maxDownloadsPerWindow');
        const windowControl = this.form.get('downloadWindowMinutes');

        if (!toggleControl || !maxControl || !windowControl) return;

        const applyState = (enabled: boolean) => {
            if (enabled) {
                maxControl.enable({ emitEvent: false });
                windowControl.enable({ emitEvent: false });
            } else {
                maxControl.disable({ emitEvent: false });
                windowControl.disable({ emitEvent: false });
            }
        };

        // Apply initial state
        applyState(toggleControl.value);

        toggleControl.valueChanges.subscribe(enabled => {
            applyState(enabled);
            if (this.form) {
                this.form.markAsDirty();
            }
        });
    }

    private setupAudioEncryptionToggle(): void {
        if (!this.form) return;

        const apiKeyControl = this.form.get('relayServerAPIKey');
        const encryptionControl = this.form.get('audioEncryptionEnabled');

        if (!apiKeyControl || !encryptionControl) return;

        const applyState = (apiKey: string) => {
            if (apiKey && apiKey.trim().length > 0) {
                encryptionControl.enable({ emitEvent: false });
            } else {
                // No API key — force off and disable
                encryptionControl.setValue(false, { emitEvent: false });
                encryptionControl.disable({ emitEvent: false });
            }
        };

        applyState(apiKeyControl.value);

        apiKeyControl.valueChanges.subscribe(apiKey => applyState(apiKey));
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
            maxHeight: '90vh',
            data: {
                relayServerURL: relayServerURL,
                existingAPIKey: existingAPIKey || null
            }
        });

        dialogRef.afterClosed().subscribe((apiKey: string | null) => {
            if (apiKey && this.form) {
                this.form.get('relayServerAPIKey')?.setValue(apiKey);
                this.saveRequested.emit();
            }
        });
    }

    recoverRelayAPIKey() {
        if (!this.form) return;

        const relayServerURL = 'https://tlradioserver.thinlineds.com';

        const dialogRef = this.dialog.open(RecoverAPIKeyDialogComponent, {
            width: '600px',
            data: { relayServerURL: relayServerURL }
        });

        dialogRef.afterClosed().subscribe((apiKey: string | null) => {
            if (apiKey && this.form) {
                this.form.get('relayServerAPIKey')?.setValue(apiKey);
                this.saveRequested.emit();
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

    private updateEmailLogoUrl(): void {
        if (this.hasEmailLogo()) {
            this.emailLogoUrl = `${window.location.origin}/email-logo?t=${Date.now()}`;
        } else {
            this.emailLogoUrl = '';
        }
    }

    // Email logo methods
    emailLogoUrl: string = '';
    private emailLogoErrorRetryCount: number = 0;
    private readonly MAX_EMAIL_LOGO_RETRIES: number = 1;

    hasEmailLogo(): boolean {
        return !!(this.form?.get('emailLogoFilename')?.value);
    }

    getEmailLogoPreview(): string {
        if (this.emailLogoUrl) {
            return this.emailLogoUrl;
        }
        return `${window.location.origin}/email-logo?t=${Date.now()}`;
    }

    getEmailLogoStyle(): string {
        const borderRadius = this.form?.get('emailLogoBorderRadius')?.value || '0px';
        return `max-width: 100%; max-height: 105px; display: block; border-radius: ${borderRadius};`;
    }

    onEmailLogoSelected(event: Event): void {
        const input = event.target as HTMLInputElement;
        if (input.files && input.files.length > 0) {
            const file = input.files[0];
            
            if (!file.type.match(/^image\/(png|jpeg|jpg|svg\+xml)$/)) {
                alert('Please select a PNG, JPG, or SVG image file.');
                return;
            }

            if (file.size > 5000000) {
                alert('Logo file size must be less than 5MB.');
                return;
            }

            if (file.type === 'image/svg+xml') {
                this.uploadEmailLogo(file);
            } else {
                this.compressAndUploadEmailLogo(file);
            }
        }
    }

    private compressAndUploadEmailLogo(file: File): void {
        const reader = new FileReader();
        reader.onload = (e: any) => {
            const img = new Image();
            img.onload = () => {
                let width = img.width;
                let height = img.height;
                const maxSize = 300;
                
                if (width > maxSize || height > maxSize) {
                    if (width > height) {
                        height = (height / width) * maxSize;
                        width = maxSize;
                    } else {
                        width = (width / height) * maxSize;
                        height = maxSize;
                    }
                }

                const canvas = document.createElement('canvas');
                canvas.width = width;
                canvas.height = height;
                const ctx = canvas.getContext('2d');
                if (!ctx) {
                    alert('Failed to process image.');
                    return;
                }
                ctx.drawImage(img, 0, 0, width, height);

                canvas.toBlob((blob) => {
                    if (!blob) {
                        alert('Failed to compress image.');
                        return;
                    }

                    if (blob.size > 500000 && file.type !== 'image/png') {
                        canvas.toBlob((compressedBlob) => {
                            if (compressedBlob) {
                                this.uploadEmailLogo(compressedBlob, file.name);
                            } else {
                                this.uploadEmailLogo(blob, file.name);
                            }
                        }, 'image/jpeg', 0.7);
                    } else {
                        this.uploadEmailLogo(blob, file.name);
                    }
                }, file.type === 'image/png' ? 'image/png' : 'image/jpeg', file.type === 'image/png' ? 1.0 : 0.85);
            };
            img.onerror = () => alert('Failed to load image.');
            img.src = e.target.result;
        };
        reader.onerror = () => alert('Failed to read file.');
        reader.readAsDataURL(file);
    }

    private uploadEmailLogo(file: File | Blob, originalName?: string): void {
        const formData = new FormData();
        const fileToUpload = file instanceof File ? file : new File([file], originalName || 'logo.jpg', { type: file.type || 'image/jpeg' });
        formData.append('logo', fileToUpload);

        const token = sessionStorage.getItem('rdio-scanner-admin-token');
        if (!token) {
            alert('Not authenticated. Please log in again.');
            return;
        }

        const headers = new HttpHeaders({ 'Authorization': token });

        this.http.post(`${window.location.origin}/api/admin/email-logo`, formData, { headers })
            .subscribe({
                next: (response: any) => {
                    if (response.success && response.filename) {
                        this.form?.get('emailLogoFilename')?.setValue(response.filename, { emitEvent: false });
                        this.emailLogoErrorRetryCount = 0;
                        this.emailLogoUrl = `${window.location.origin}/email-logo?t=${Date.now()}`;
                        this.cdr.detectChanges();
                        this.snackBar.open('Email logo uploaded successfully', 'Close', { duration: 3000 });
                        this.form?.markAsDirty();
                    } else {
                        alert('Failed to upload logo: ' + (response.error || 'Unknown error'));
                    }
                },
                error: (error) => {
                    console.error('Logo upload error:', error);
                    let errorMsg = 'Failed to upload logo.';
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

    removeEmailLogo(): void {
        const token = sessionStorage.getItem('rdio-scanner-admin-token');
        if (!token) {
            alert('Not authenticated. Please log in again.');
            return;
        }

        const headers = new HttpHeaders({ 'Authorization': token });

        this.http.delete(`${window.location.origin}/api/admin/email-logo/delete`, { headers })
            .subscribe({
                next: (response: any) => {
                    if (response.success) {
                        this.form?.get('emailLogoFilename')?.setValue('', { emitEvent: false });
                        this.emailLogoUrl = '';
                        this.emailLogoErrorRetryCount = 0;
                        this.cdr.detectChanges();
                        this.snackBar.open('Email logo removed successfully', 'Close', { duration: 3000 });
                        this.form?.markAsDirty();
                    } else {
                        alert('Failed to remove logo: ' + (response.error || 'Unknown error'));
                    }
                },
                error: (error) => {
                    console.error('Logo removal error:', error);
                    alert('Failed to remove logo: ' + (error.error?.error || error.message || 'Unknown error'));
                }
            });
    }

    onEmailLogoLoad(): void {
        this.emailLogoErrorRetryCount = 0;
        this.cdr.detectChanges();
    }

    onEmailLogoError(): void {
        if (this.emailLogoUrl && this.emailLogoErrorRetryCount < this.MAX_EMAIL_LOGO_RETRIES) {
            this.emailLogoErrorRetryCount++;
            const url = new URL(this.emailLogoUrl);
            url.searchParams.set('t', Date.now().toString());
            this.emailLogoUrl = url.toString();
            this.cdr.detectChanges();
        } else {
            this.emailLogoUrl = '';
            this.emailLogoErrorRetryCount = 0;
            this.cdr.detectChanges();
        }
    }

    // Test email functionality
    testEmailAddress: string = '';
    sendingTestEmail: boolean = false;
    testEmailError: string = '';
    testEmailSuccess: string = '';

    sendTestEmail(): void {
        if (!this.testEmailAddress || !this.testEmailAddress.trim()) {
            this.testEmailError = 'Please enter a recipient email address';
            this.testEmailSuccess = '';
            return;
        }

        const emailRegex = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;
        if (!emailRegex.test(this.testEmailAddress)) {
            this.testEmailError = 'Please enter a valid email address';
            this.testEmailSuccess = '';
            return;
        }

        const token = sessionStorage.getItem('rdio-scanner-admin-token');
        if (!token) {
            this.testEmailError = 'Not authenticated. Please log in again.';
            this.testEmailSuccess = '';
            return;
        }

        this.sendingTestEmail = true;
        this.testEmailError = '';
        this.testEmailSuccess = '';

        const headers = new HttpHeaders({
            'Authorization': token,
            'Content-Type': 'application/json'
        });

        this.http.post(`${window.location.origin}/api/admin/email-test`, 
            { toEmail: this.testEmailAddress.trim() }, 
            { headers })
            .subscribe({
                next: (response: any) => {
                    this.sendingTestEmail = false;
                    if (response.success) {
                        this.testEmailSuccess = response.message || 'Test email sent successfully!';
                        this.testEmailError = '';
                    } else {
                        this.testEmailError = response.error || 'Failed to send test email';
                        this.testEmailSuccess = '';
                    }
                    this.cdr.detectChanges();
                },
                error: (error) => {
                    this.sendingTestEmail = false;
                    console.error('Test email error:', error);
                    let errorMsg = 'Failed to send test email.';
                    
                    if (error.error) {
                        if (typeof error.error === 'string') {
                            errorMsg = error.error;
                        } else if (error.error.error) {
                            errorMsg = error.error.error;
                        } else if (error.error.message) {
                            errorMsg = error.error.message;
                        }
                    } else if (error.message) {
                        errorMsg = error.message;
                    }
                    
                    if (errorMsg === 'Failed to send test email.') {
                        if (error.status === 0) {
                            errorMsg = 'Connection error. Please check your network connection.';
                        } else if (error.status === 401) {
                            errorMsg = 'Authentication failed. Please log in again.';
                        } else if (error.status === 500) {
                            errorMsg = 'Server error occurred. Check server logs for details.';
                        }
                    }
                    
                    this.testEmailError = errorMsg;
                    this.testEmailSuccess = '';
                    this.cdr.detectChanges();
                }
            });
    }

    generateExternalAPIKey(): void {
        if (!this.form) return;
        const chars = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789';
        let key = '';
        const array = new Uint8Array(48);
        window.crypto.getRandomValues(array);
        array.forEach(b => key += chars[b % chars.length]);
        this.form.get('centralManagementAPIKey')?.setValue(key);
        this.form.markAsDirty();
        this.snackBar.open('New API key generated — save your configuration to apply it.', 'Close', { duration: 5000 });
    }

    async generateFaviconFromLogo(): Promise<void> {
        if (!this.hasEmailLogo() || !this.emailLogoUrl) {
            this.snackBar.open('No server logo found. Please upload a server logo first.', 'Close', { duration: 3000 });
            return;
        }

        try {
            // Create an image element to load the logo
            const img = new Image();
            img.crossOrigin = 'anonymous';
            
            await new Promise<void>((resolve, reject) => {
                img.onload = () => resolve();
                img.onerror = () => reject(new Error('Failed to load server logo'));
                img.src = this.emailLogoUrl;
            });

            // Create a canvas to resize the logo to favicon size (32x32)
            const canvas = document.createElement('canvas');
            canvas.width = 32;
            canvas.height = 32;
            const ctx = canvas.getContext('2d');
            
            if (!ctx) {
                throw new Error('Failed to create canvas context');
            }

            // Get the border radius setting
            const borderRadius = this.form?.get('emailLogoBorderRadius')?.value || '0px';
            
            // Parse border radius (handle px, %, or unitless numbers)
            let radius = 0;
            if (borderRadius.includes('%')) {
                // If percentage, apply to 32x32 canvas
                const percent = parseFloat(borderRadius);
                radius = (32 * percent) / 100;
            } else {
                // Parse as pixels
                radius = parseFloat(borderRadius) || 0;
                // Scale the radius proportionally if the original logo is larger
                // Assume typical logo is ~200px, scale to 32px
                radius = (radius * 32) / 200;
            }

            // Apply border radius clipping if set
            if (radius > 0) {
                ctx.beginPath();
                
                // Create rounded rectangle path
                const x = 0, y = 0, width = 32, height = 32;
                ctx.moveTo(x + radius, y);
                ctx.lineTo(x + width - radius, y);
                ctx.quadraticCurveTo(x + width, y, x + width, y + radius);
                ctx.lineTo(x + width, y + height - radius);
                ctx.quadraticCurveTo(x + width, y + height, x + width - radius, y + height);
                ctx.lineTo(x + radius, y + height);
                ctx.quadraticCurveTo(x, y + height, x, y + height - radius);
                ctx.lineTo(x, y + radius);
                ctx.quadraticCurveTo(x, y, x + radius, y);
                ctx.closePath();
                
                ctx.clip();
            }

            // Draw the image scaled to 32x32
            ctx.drawImage(img, 0, 0, 32, 32);

            // Convert canvas to blob
            const blob = await new Promise<Blob>((resolve, reject) => {
                canvas.toBlob((b) => {
                    if (b) resolve(b);
                    else reject(new Error('Failed to create favicon blob'));
                }, 'image/png');
            });

            // Create a file from the blob
            const file = new File([blob], 'favicon.png', { type: 'image/png' });

            // Upload the favicon
            await this.uploadFavicon(file);

            this.snackBar.open('Favicon generated successfully from server logo!', 'Close', { duration: 3000 });
        } catch (error) {
            console.error('Error generating favicon:', error);
            this.snackBar.open('Failed to generate favicon. Please try uploading manually.', 'Close', { duration: 5000 });
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

    // Helper methods for array handling in templates
    getAssemblyAIWordBoostDisplay(): string {
        const wordBoost = this.form?.get('transcriptionConfig')?.get('assemblyAIWordBoost')?.value;
        return Array.isArray(wordBoost) ? wordBoost.join(',') : '';
    }

    setAssemblyAIWordBoost(value: string): void {
        const terms = value.split(',').map(s => s.trim()).filter(s => s);
        this.form?.get('transcriptionConfig')?.get('assemblyAIWordBoost')?.setValue(terms);
    }

    getHallucinationPatternsDisplay(): string {
        const patterns = this.form?.get('transcriptionConfig')?.get('hallucinationPatterns')?.value;
        return Array.isArray(patterns) ? patterns.join('\n') : '';
    }

    setHallucinationPatterns(value: string): void {
        const patterns = value.split('\n').map(s => s.trim()).filter(s => s);
        this.form?.get('transcriptionConfig')?.get('hallucinationPatterns')?.setValue(patterns);
    }
}
