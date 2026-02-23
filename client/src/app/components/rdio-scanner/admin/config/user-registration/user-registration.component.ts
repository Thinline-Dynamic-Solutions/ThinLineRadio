/*
 * *****************************************************************************
 * Copyright (C) 2025 Thinline Dynamic Solutions
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

import { Component, Input, OnInit, OnChanges, SimpleChanges, ChangeDetectorRef } from '@angular/core';
import { FormGroup, FormBuilder, FormControl } from '@angular/forms';
import { HttpClient, HttpHeaders } from '@angular/common/http';

@Component({
  selector: 'rdio-scanner-admin-user-registration',
  templateUrl: './user-registration.component.html',
  styleUrls: ['./user-registration.component.scss']
})
export class RdioScannerAdminUserRegistrationComponent implements OnInit, OnChanges {
  @Input() form!: FormGroup;
  userRegistrationForm!: FormGroup;
  logoUrl: string = '';
  private isSyncing: boolean = false;
  private imageErrorRetryCount: number = 0;
  private readonly MAX_IMAGE_RETRIES: number = 1;
  testEmailAddress: string = '';
  sendingTestEmail: boolean = false;
  testEmailError: string = '';
  testEmailSuccess: string = '';
  window = window; // Expose window to template
  hasPublicRegistrationGroup: boolean = false;
  registrationModeValue: string = 'invite'; // 'invite', 'public', or 'centralized'
  centralConnectionStatus: string = '';
  centralConnectionMessage: string = '';

  constructor(private fb: FormBuilder, private http: HttpClient, private cdr: ChangeDetectorRef) {}

  ngOnInit() {
    // Load groups to check for public registration group
    this.loadGroups();

    // Create a standalone form for user registration
    this.userRegistrationForm = this.fb.group({
      userRegistrationEnabled: new FormControl(true), // Always enabled now
      publicRegistrationEnabled: new FormControl(false), // Default to false (invite-only)
      publicRegistrationMode: new FormControl('both'),
      stripePaywallEnabled: new FormControl(false),
      emailServiceEnabled: new FormControl(false),
      emailProvider: new FormControl('sendgrid'),
      emailSmtpFromEmail: new FormControl(''),
      emailSmtpFromName: new FormControl(''),
      emailSendGridApiKey: new FormControl(''),
      emailMailgunApiKey: new FormControl(''),
      emailMailgunDomain: new FormControl(''),
      emailMailgunApiBase: new FormControl('https://api.mailgun.net'),
      emailSmtpHost: new FormControl(''),
      emailSmtpPort: new FormControl(587),
      emailSmtpUsername: new FormControl(''),
      emailSmtpPassword: new FormControl(''),
      emailSmtpUseTLS: new FormControl(true),
      emailSmtpSkipVerify: new FormControl(false),
      emailLogoFilename: new FormControl(''),
      emailLogoBorderRadius: new FormControl('0px'),
      stripePublishableKey: new FormControl(''),
      stripeSecretKey: new FormControl(''),
      stripeWebhookSecret: new FormControl(''),
      stripeGracePeriodDays: new FormControl(0),
      baseUrl: new FormControl(''),
      registrationMode: new FormControl('invite'), // New field for the UI dropdown
      centralManagementServerName: new FormControl(''),
      centralManagementServerID: new FormControl(''),
      centralManagementUrl: new FormControl(''),
      centralManagementApiKey: new FormControl(''),
    });

    // Update logo URL when filename changes (but debounce to avoid loops)
    this.userRegistrationForm.get('emailLogoFilename')?.valueChanges.subscribe((filename) => {
      if (!this.isSyncing && filename) {
        // Only update if we have a filename and we're not syncing
        // Reset retry counter when setting a new logo URL
        this.imageErrorRetryCount = 0;
        this.logoUrl = `${window.location.origin}/email-logo?t=${Date.now()}`;
      } else if (!this.isSyncing && !filename) {
        // Clear logo URL if filename is empty
        this.logoUrl = '';
        this.imageErrorRetryCount = 0;
      }
    });

    // Handle registration mode changes
    this.userRegistrationForm.get('registrationMode')?.valueChanges.subscribe((mode) => {
      if (!this.isSyncing) {
        // Map UI dropdown to backend fields
        if (mode === 'public') {
          this.userRegistrationForm.patchValue({
            publicRegistrationEnabled: true,
            publicRegistrationMode: 'both' // Always default to both codes and email
          }, { emitEvent: false });
        } else if (mode === 'centralized') {
          // Disable local user management when centralized is selected
          this.userRegistrationForm.patchValue({
            publicRegistrationEnabled: false,
            stripePaywallEnabled: false,
            emailServiceEnabled: false
          }, { emitEvent: false });
        } else { // 'invite'
          this.userRegistrationForm.patchValue({
            publicRegistrationEnabled: false
          }, { emitEvent: false });
        }
        this.syncToParentForm();
      }
    });
    
    // Sync from parent form when it's available
    if (this.form) {
      this.syncFromParentForm();
      
      // Watch for changes in the parent form (debounced to prevent loops)
      let syncTimeout: any;
      this.form.valueChanges.subscribe(() => {
        if (syncTimeout) {
          clearTimeout(syncTimeout);
        }
        syncTimeout = setTimeout(() => {
          if (!this.isSyncing) {
            this.syncFromParentForm();
          }
        }, 100);
      });
    } else {
      // If no parent form yet, still update logo URL in case filename is set
      setTimeout(() => {
        this.updateLogoUrl();
      }, 100);
    }

    // Subscribe to form changes to automatically sync (debounced to prevent loops)
    let syncToParentTimeout: any;
    this.userRegistrationForm.valueChanges.subscribe(() => {
      if (syncToParentTimeout) {
        clearTimeout(syncToParentTimeout);
      }
      syncToParentTimeout = setTimeout(() => {
        if (!this.isSyncing) {
          this.syncToParentForm();
        }
      }, 100);
    });
  }

  ngOnChanges(changes: SimpleChanges) {
    // When the form input changes (e.g., during config import), re-sync
    if (changes['form'] && !changes['form'].firstChange && this.userRegistrationForm) {
      this.syncFromParentForm();
      this.cdr.detectChanges();
    }
  }

  private syncFromParentForm() {
    if (this.isSyncing) {
      return; // Prevent infinite loops
    }
    this.isSyncing = true;
    
    try {
      // Check if we received the options form group directly
      if (this.form && this.form.get('userRegistrationEnabled') !== null) {
        // This is the options form group directly
        const publicRegEnabled = this.form.get('publicRegistrationEnabled')?.value ?? false;
        const centralEnabled = this.form.get('centralManagementEnabled')?.value ?? false;
        
        // Determine registration mode
        let registrationMode = 'invite';
        if (centralEnabled) {
          registrationMode = 'centralized';
        } else if (publicRegEnabled) {
          registrationMode = 'public';
        }

        this.userRegistrationForm.patchValue({
          userRegistrationEnabled: true, // Always true now
          publicRegistrationEnabled: publicRegEnabled,
          publicRegistrationMode: this.form.get('publicRegistrationMode')?.value || 'both',
          stripePaywallEnabled: this.form.get('stripePaywallEnabled')?.value || false,
          emailServiceEnabled: this.form.get('emailServiceEnabled')?.value || false,
          emailProvider: this.form.get('emailProvider')?.value || 'sendgrid',
          emailSmtpFromEmail: this.form.get('emailSmtpFromEmail')?.value || '',
          emailSmtpFromName: this.form.get('emailSmtpFromName')?.value || '',
          emailSendGridApiKey: this.form.get('emailSendGridApiKey')?.value || '',
          emailMailgunApiKey: this.form.get('emailMailgunApiKey')?.value || '',
          emailMailgunDomain: this.form.get('emailMailgunDomain')?.value || '',
          emailMailgunApiBase: this.form.get('emailMailgunApiBase')?.value || 'https://api.mailgun.net',
          emailSmtpHost: this.form.get('emailSmtpHost')?.value || '',
          emailSmtpPort: this.form.get('emailSmtpPort')?.value || 587,
          emailSmtpUsername: this.form.get('emailSmtpUsername')?.value || '',
          emailSmtpPassword: this.form.get('emailSmtpPassword')?.value || '',
          emailSmtpUseTLS: this.form.get('emailSmtpUseTLS')?.value ?? true,
          emailSmtpSkipVerify: this.form.get('emailSmtpSkipVerify')?.value || false,
          emailLogoFilename: this.form.get('emailLogoFilename')?.value || '',
          emailLogoBorderRadius: this.form.get('emailLogoBorderRadius')?.value ?? '0px',
          stripePublishableKey: this.form.get('stripePublishableKey')?.value || '',
          stripeSecretKey: this.form.get('stripeSecretKey')?.value || '',
          stripeWebhookSecret: this.form.get('stripeWebhookSecret')?.value || '',
          stripeGracePeriodDays: this.form.get('stripeGracePeriodDays')?.value || 0,
          baseUrl: this.form.get('baseUrl')?.value || '',
          registrationMode: registrationMode,
          centralManagementServerName: this.form.get('centralManagementServerName')?.value || '',
          centralManagementServerID: this.form.get('centralManagementServerID')?.value || '',
          centralManagementUrl: this.form.get('centralManagementURL')?.value || '',
          centralManagementApiKey: this.form.get('centralManagementAPIKey')?.value || '',
        }, { emitEvent: false }); // Don't emit events to prevent loops
      } else if (this.form && this.form.get('options')) {
        // This is the full form, get the options form group
        const options = this.form.get('options');
        if (options) {
          const publicRegEnabled = options.get('publicRegistrationEnabled')?.value ?? false;
          const centralEnabled = options.get('centralManagementEnabled')?.value ?? false;
          
          // Determine registration mode
          let registrationMode = 'invite';
          if (centralEnabled) {
            registrationMode = 'centralized';
          } else if (publicRegEnabled) {
            registrationMode = 'public';
          }
          
          this.userRegistrationForm.patchValue({
            userRegistrationEnabled: true, // Always true now
            publicRegistrationEnabled: publicRegEnabled,
            publicRegistrationMode: options.get('publicRegistrationMode')?.value || 'both',
            stripePaywallEnabled: options.get('stripePaywallEnabled')?.value || false,
            emailServiceEnabled: options.get('emailServiceEnabled')?.value || false,
            emailProvider: options.get('emailProvider')?.value || 'sendgrid',
            emailSmtpFromEmail: options.get('emailSmtpFromEmail')?.value || '',
            emailSmtpFromName: options.get('emailSmtpFromName')?.value || '',
            emailSendGridApiKey: options.get('emailSendGridApiKey')?.value || '',
            emailMailgunApiKey: options.get('emailMailgunApiKey')?.value || '',
            emailMailgunDomain: options.get('emailMailgunDomain')?.value || '',
            emailMailgunApiBase: options.get('emailMailgunApiBase')?.value || 'https://api.mailgun.net',
            emailSmtpHost: options.get('emailSmtpHost')?.value || '',
            emailSmtpPort: options.get('emailSmtpPort')?.value || 587,
            emailSmtpUsername: options.get('emailSmtpUsername')?.value || '',
            emailSmtpPassword: options.get('emailSmtpPassword')?.value || '',
            emailSmtpUseTLS: options.get('emailSmtpUseTLS')?.value ?? true,
            emailSmtpSkipVerify: options.get('emailSmtpSkipVerify')?.value || false,
            emailLogoFilename: options.get('emailLogoFilename')?.value || '',
            emailLogoBorderRadius: options.get('emailLogoBorderRadius')?.value ?? '0px',
            stripePublishableKey: options.get('stripePublishableKey')?.value || '',
            stripeSecretKey: options.get('stripeSecretKey')?.value || '',
            stripeWebhookSecret: options.get('stripeWebhookSecret')?.value || '',
            stripeGracePeriodDays: options.get('stripeGracePeriodDays')?.value || 0,
            baseUrl: options.get('baseUrl')?.value || '',
            registrationMode: registrationMode,
            centralManagementServerName: options.get('centralManagementServerName')?.value || '',
            centralManagementServerID: options.get('centralManagementServerID')?.value || '',
            centralManagementUrl: options.get('centralManagementURL')?.value || '',
            centralManagementApiKey: options.get('centralManagementAPIKey')?.value || '',
          }, { emitEvent: false }); // Don't emit events to prevent loops
        }
      }
      
      // Update logo URL after syncing (only once, not in a loop)
      setTimeout(() => {
        this.updateLogoUrl();
      }, 0);
    } finally {
      this.isSyncing = false;
    }
  }

  // Method to sync changes back to parent form
  syncToParentForm() {
    if (this.isSyncing || !this.form) {
      return; // Prevent infinite loops
    }
    
    // Check if we received the options form group directly
    if (this.form.get('userRegistrationEnabled') !== null) {
      // This is the options form group directly - update each field individually
      const values = this.userRegistrationForm.value;
      
      // Determine if centralized management is enabled based on registration mode
      const isCentralized = values.registrationMode === 'centralized';
      
      this.form.patchValue({
        userRegistrationEnabled: true, // Always true now
        publicRegistrationEnabled: values.publicRegistrationEnabled,
        publicRegistrationMode: values.publicRegistrationMode,
        stripePaywallEnabled: values.stripePaywallEnabled,
        emailServiceEnabled: values.emailServiceEnabled,
        emailProvider: values.emailProvider,
        emailSmtpFromEmail: values.emailSmtpFromEmail,
        emailSmtpFromName: values.emailSmtpFromName,
        emailSendGridApiKey: values.emailSendGridApiKey,
        emailMailgunApiKey: values.emailMailgunApiKey,
        emailMailgunDomain: values.emailMailgunDomain,
        emailMailgunApiBase: values.emailMailgunApiBase,
        emailSmtpHost: values.emailSmtpHost,
        emailSmtpPort: values.emailSmtpPort,
        emailSmtpUsername: values.emailSmtpUsername,
        emailSmtpPassword: values.emailSmtpPassword,
        emailSmtpUseTLS: values.emailSmtpUseTLS,
        emailSmtpSkipVerify: values.emailSmtpSkipVerify,
        emailLogoFilename: values.emailLogoFilename,
        emailLogoBorderRadius: values.emailLogoBorderRadius,
        stripePublishableKey: values.stripePublishableKey,
        stripeSecretKey: values.stripeSecretKey,
        stripeWebhookSecret: values.stripeWebhookSecret,
        stripeGracePeriodDays: values.stripeGracePeriodDays,
        baseUrl: values.baseUrl,
        // Centralized management fields
        centralManagementEnabled: isCentralized,
        centralManagementServerName: values.centralManagementServerName || '',
        centralManagementServerID: values.centralManagementServerID || '',
        centralManagementURL: values.centralManagementUrl || '',
        centralManagementAPIKey: values.centralManagementApiKey || '',
      }, { emitEvent: false });
      
      this.form.markAsDirty();
      this.form.markAsDirty();
    } else if (this.form.get('options')) {
      // This is the full form, get the options form group
      const options = this.form.get('options');
      if (options) {
        const values = this.userRegistrationForm.value;
        
        // Determine if centralized management is enabled based on registration mode
        const isCentralized = values.registrationMode === 'centralized';
        
        options.patchValue({
          userRegistrationEnabled: true, // Always true now
          publicRegistrationEnabled: values.publicRegistrationEnabled,
          publicRegistrationMode: values.publicRegistrationMode,
          stripePaywallEnabled: values.stripePaywallEnabled,
          emailServiceEnabled: values.emailServiceEnabled,
          emailProvider: values.emailProvider,
          emailSmtpFromEmail: values.emailSmtpFromEmail,
          emailSmtpFromName: values.emailSmtpFromName,
          emailSendGridApiKey: values.emailSendGridApiKey,
          emailMailgunApiKey: values.emailMailgunApiKey,
          emailMailgunDomain: values.emailMailgunDomain,
          emailMailgunApiBase: values.emailMailgunApiBase,
          emailSmtpHost: values.emailSmtpHost,
          emailSmtpPort: values.emailSmtpPort,
          emailSmtpUsername: values.emailSmtpUsername,
          emailSmtpPassword: values.emailSmtpPassword,
          emailSmtpUseTLS: values.emailSmtpUseTLS,
          emailSmtpSkipVerify: values.emailSmtpSkipVerify,
          emailLogoFilename: values.emailLogoFilename,
          emailLogoBorderRadius: values.emailLogoBorderRadius,
          stripePublishableKey: values.stripePublishableKey,
          stripeSecretKey: values.stripeSecretKey,
          stripeWebhookSecret: values.stripeWebhookSecret,
          stripeGracePeriodDays: values.stripeGracePeriodDays || 0,
          baseUrl: values.baseUrl,
          // Centralized management fields
          centralManagementEnabled: isCentralized,
          centralManagementServerName: values.centralManagementServerName,
          centralManagementServerID: values.centralManagementServerID,
          centralManagementURL: values.centralManagementUrl,
          centralManagementAPIKey: values.centralManagementApiKey,
        }, { emitEvent: false }); // Don't emit events to prevent loops
      }
    }
  }

  onLogoSelected(event: any): void {
    const file = event.target.files[0];
    if (file) {
      // Validate file type
      if (!file.type.match(/^image\/(png|jpeg|jpg|svg\+xml)$/)) {
        alert('Please select a PNG, JPG, or SVG image file.');
        return;
      }

      // Validate file size (max 5MB)
      if (file.size > 5000000) {
        alert('Logo file size must be less than 5MB.');
        return;
      }

      // Process and upload file
      if (file.type === 'image/svg+xml') {
        // SVG files: upload directly
        this.uploadLogo(file);
      } else {
        // PNG/JPG files: compress before upload
        this.compressAndUpload(file);
      }
    }
  }

  private compressAndUpload(file: File): void {
    const reader = new FileReader();
    reader.onload = (e: any) => {
      const img = new Image();
      img.onload = () => {
        // Calculate new dimensions (max 300px width or height)
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

        // Create canvas and draw resized image
        const canvas = document.createElement('canvas');
        canvas.width = width;
        canvas.height = height;
        const ctx = canvas.getContext('2d');
        if (!ctx) {
          alert('Failed to process image.');
          return;
        }
        ctx.drawImage(img, 0, 0, width, height);

        // Convert to blob with compression
        canvas.toBlob((blob) => {
          if (!blob) {
            alert('Failed to compress image.');
            return;
          }

          // If still too large (>500KB), compress more aggressively
          if (blob.size > 500000 && file.type !== 'image/png') {
            // For JPEG, try lower quality
            canvas.toBlob((compressedBlob) => {
              if (compressedBlob) {
                this.uploadLogo(compressedBlob, file.name);
              } else {
                this.uploadLogo(blob, file.name);
              }
            }, 'image/jpeg', 0.7);
          } else {
            this.uploadLogo(blob, file.name);
          }
        }, file.type === 'image/png' ? 'image/png' : 'image/jpeg', file.type === 'image/png' ? 1.0 : 0.85);
      };
      img.onerror = () => {
        alert('Failed to load image.');
      };
      img.src = e.target.result;
    };
    reader.onerror = () => {
      alert('Failed to read file.');
    };
    reader.readAsDataURL(file);
  }

  private uploadLogo(file: File | Blob, originalName?: string): void {
    const formData = new FormData();
    // Create a File object from Blob if needed
    const fileToUpload = file instanceof File ? file : new File([file], originalName || 'logo.jpg', { type: file.type || 'image/jpeg' });
    formData.append('logo', fileToUpload);

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
    
    console.log('Uploading logo with token:', token ? 'Token present (' + token.substring(0, 20) + '...)' : 'No token');

    this.http.post(`${window.location.origin}/api/admin/email-logo`, formData, { headers })
      .subscribe({
        next: (response: any) => {
          if (response.success && response.filename) {
            // Update form with filename (without emitting events to prevent loops)
            this.userRegistrationForm.get('emailLogoFilename')?.setValue(response.filename, { emitEvent: false });
            // Update logo preview URL immediately with cache busting
            // Reset retry counter when setting a new logo URL after upload
            this.imageErrorRetryCount = 0;
            this.logoUrl = `${window.location.origin}/email-logo?t=${Date.now()}`;
            // Force change detection to update the UI
            this.cdr.detectChanges();
            // Sync to parent form
            this.syncToParentForm();
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

  removeLogo(): void {
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

    this.http.delete(`${window.location.origin}/api/admin/email-logo/delete`, { headers })
      .subscribe({
        next: (response: any) => {
          if (response.success) {
            this.userRegistrationForm.get('emailLogoFilename')?.setValue('');
            this.logoUrl = '';
            // Reset retry counter when clearing logo
            this.imageErrorRetryCount = 0;
            // Force change detection to update the UI
            this.cdr.detectChanges();
            // Sync to parent form
            this.syncToParentForm();
          } else {
            alert('Failed to delete logo: ' + (response.error || 'Unknown error'));
          }
        },
        error: (error) => {
          console.error('Logo delete error:', error);
          alert('Failed to delete logo. Please try again.');
        }
      });
  }

  hasLogo(): boolean {
    return !!this.logoUrl;
  }

  getLogoPreview(): string {
    return this.logoUrl;
  }

  getLogoStyle(): string {
    const borderRadius = this.userRegistrationForm.get('emailLogoBorderRadius')?.value || '0px';
    return `max-width: 100%; max-height: 105px; display: block; border-radius: ${borderRadius};`;
  }

  onImageLoad(): void {
    // Image loaded successfully - reset retry counter
    this.imageErrorRetryCount = 0;
    this.cdr.detectChanges();
  }

  onImageError(): void {
    // If image fails to load, try refreshing the URL with a new timestamp (max 1 retry)
    if (this.logoUrl && this.imageErrorRetryCount < this.MAX_IMAGE_RETRIES) {
      this.imageErrorRetryCount++;
      const url = new URL(this.logoUrl);
      url.searchParams.set('t', Date.now().toString());
      this.logoUrl = url.toString();
      this.cdr.detectChanges();
    } else {
      // After max retries, clear the logo URL to prevent infinite loop
      // This happens when the file doesn't exist on the server
      this.logoUrl = '';
      this.imageErrorRetryCount = 0;
      this.cdr.detectChanges();
    }
  }

  private updateLogoUrl(): void {
    const filename = this.userRegistrationForm.get('emailLogoFilename')?.value;
    if (filename && filename.trim() !== '') {
      // Reset retry counter when setting a new logo URL
      this.imageErrorRetryCount = 0;
      this.logoUrl = `${window.location.origin}/email-logo?t=${Date.now()}`;
    } else {
      this.logoUrl = '';
      this.imageErrorRetryCount = 0;
    }
  }

  sendTestEmail(): void {
    if (!this.testEmailAddress || !this.testEmailAddress.trim()) {
      this.testEmailError = 'Please enter a recipient email address';
      this.testEmailSuccess = '';
      return;
    }

    // Validate email format
    const emailRegex = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;
    if (!emailRegex.test(this.testEmailAddress)) {
      this.testEmailError = 'Please enter a valid email address';
      this.testEmailSuccess = '';
      return;
    }

    // Get auth token from session storage
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
          
          // Try to extract error message from various possible locations
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
          
          // Provide more specific messages based on status code if no detailed error was found
          if (errorMsg === 'Failed to send test email.') {
            if (error.status === 0) {
              errorMsg = 'Connection error. Please check your network connection.';
            } else if (error.status === 401) {
              errorMsg = 'Authentication failed. Please log in again.';
            } else if (error.status === 500) {
              errorMsg = 'Server error occurred. Check the error details above.';
            }
          }
          
          this.testEmailError = errorMsg;
          this.testEmailSuccess = '';
          this.cdr.detectChanges();
        }
      });
  }

  testCentralConnection(): void {
    const url = this.userRegistrationForm.get('centralManagementUrl')?.value;
    const apiKey = this.userRegistrationForm.get('centralManagementApiKey')?.value;
    const serverName = this.userRegistrationForm.get('centralManagementServerName')?.value;
    const baseUrl = this.userRegistrationForm.get('baseUrl')?.value;

    if (!url || !apiKey) {
      this.centralConnectionStatus = 'error';
      this.centralConnectionMessage = 'Please enter both URL and API Key';
      return;
    }

    this.centralConnectionStatus = 'testing';
    this.centralConnectionMessage = 'Testing connection...';

    // Get auth token
    const token = sessionStorage.getItem('rdio-scanner-admin-token');
    if (!token) {
      this.centralConnectionStatus = 'error';
      this.centralConnectionMessage = 'Not authenticated. Please log in again.';
      return;
    }

    // Call backend endpoint to test connection (backend will test the connection to central system)
    this.http.post(`${window.location.origin}/api/admin/test-central-connection`, {
      central_management_url: url,
      api_key: apiKey,
      server_name: serverName || 'Unnamed Server',
      server_url: baseUrl || window.location.origin
    }, {
      headers: new HttpHeaders({
        'Authorization': token
      })
    }).subscribe({
      next: (response: any) => {
        this.centralConnectionStatus = 'success';
        this.centralConnectionMessage = response.message || '✓ Connection successful!';
        this.cdr.detectChanges();
      },
      error: (error) => {
        this.centralConnectionStatus = 'error';
        let errorMsg = 'Connection failed. ';
        
        if (error.status === 0) {
          errorMsg += 'Cannot reach TLR server backend.';
        } else if (error.status === 400) {
          errorMsg += error.error?.error || 'Central management not configured.';
        } else if (error.error?.error) {
          errorMsg += error.error.error;
        } else if (error.error?.message) {
          errorMsg += error.error.message;
        } else {
          errorMsg += `Error: ${error.message || 'Unknown error'}`;
        }
        
        this.centralConnectionMessage = errorMsg;
        this.cdr.detectChanges();
      }
    });
  }

  async loadGroups(): Promise<void> {
    try {
      // Get auth token from session storage
      const token = sessionStorage.getItem('rdio-scanner-admin-token');
      if (!token) {
        return;
      }

      const headers = new HttpHeaders({
        'Authorization': token
      });

      const response: any = await this.http.get(`${window.location.origin}/api/admin/groups`, { headers }).toPromise();
      const groups = response?.groups || [];
      
      // Check if any group has isPublicRegistration set to true
      this.hasPublicRegistrationGroup = groups.some((g: any) => g.isPublicRegistration === true);
      this.cdr.detectChanges();
    } catch (error) {
      console.error('Error loading groups:', error);
      this.hasPublicRegistrationGroup = false;
    }
  }
}
