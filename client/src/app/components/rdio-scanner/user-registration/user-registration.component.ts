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

import { Component, OnInit } from '@angular/core';
import { FormBuilder, FormGroup, Validators } from '@angular/forms';
import { HttpClient } from '@angular/common/http';
import { Router, ActivatedRoute } from '@angular/router';
import { RdioScannerService } from '../rdio-scanner.service';
import { MatSnackBar } from '@angular/material/snack-bar';

@Component({
  selector: 'rdio-scanner-user-registration',
  templateUrl: './user-registration.component.html',
  styleUrls: ['./user-registration.component.scss']
})
export class RdioScannerUserRegistrationComponent implements OnInit {
  registrationForm: FormGroup;
  loading = false;
  success = false;
  error = '';
  generatedPin: string | null = null;
  
  // Public registration info
  publicGroupInfo: any = null;
  loadingGroupInfo = false;
  availableChannels: any[] = [];
  loadingChannels = false;
  showChannels = false;
  
  // Invite only mode
  isInviteOnlyMode = true; // Default to true until we load settings
  codeValidated = false;
  pendingAccessCode = '';
  validatingCode = false;
  codeValidationError = '';
  validatedGroupInfo: any = null;
  loadingSettings = true; // Track if we're still loading settings
  
  // Email verification (two-step signup)
  emailVerificationRequired = false; // Default to false
  emailVerificationStep = false; // Track if we're on email verification step
  verificationCode = ''; // Store the 6-digit code user enters
  verifyingEmail = false;
  emailVerificationError = '';
  pendingEmail = ''; // Store email after step 1
  
  // Resend verification email rate limiting
  resendDisabled = false;
  resendCooldown = 0;
  resendInterval: any = null;
  
  // Invitation handling
  invitationCode: string | null = null;
  invitationGroupName: string | null = null;

  constructor(
    private fb: FormBuilder,
    private http: HttpClient,
    private router: Router,
    private route: ActivatedRoute,
    private rdioScannerService: RdioScannerService,
    private snackBar: MatSnackBar
  ) {
    this.registrationForm = this.fb.group({
      email: ['', [Validators.required, Validators.email]],
      password: ['', [Validators.required, Validators.minLength(8), this.passwordStrengthValidator]],
      confirmPassword: ['', [Validators.required]],
      firstName: ['', [Validators.required]],
      lastName: ['', [Validators.required]],
      zipCode: ['', [Validators.required]],
      accessCode: ['']  // Unified field for invitation and registration codes
    }, { validators: this.passwordMatchValidator });
    
    // Automatically convert email to lowercase as user types
    this.registrationForm.get('email')?.valueChanges.subscribe(value => {
      if (value && value !== value.toLowerCase()) {
        this.registrationForm.get('email')?.setValue(value.toLowerCase(), { emitEvent: false });
      }
    });
  }

  ngOnInit(): void {
    console.log('UserRegistration ngOnInit - isInviteOnlyMode initial:', this.isInviteOnlyMode);
    this.loadRegistrationSettings();
    
    // Check for invitation code in URL or sessionStorage (captured before Angular loaded)
    this.route.queryParams.subscribe(params => {
      let inviteCode = params['invite'];
      if (!inviteCode) {
        // Check if it was captured in sessionStorage before Angular loaded
        inviteCode = sessionStorage.getItem('pendingInviteCode');
        if (inviteCode) {
          console.log('Retrieved invitation code from sessionStorage:', inviteCode);
          sessionStorage.removeItem('pendingInviteCode');
        }
      }
      
      if (inviteCode) {
        this.handleInvitation(inviteCode);
        // Clear the invite parameter from URL
        const urlParams = new URLSearchParams(window.location.search);
        urlParams.delete('invite');
        const newUrl = window.location.pathname + (urlParams.toString() ? '?' + urlParams.toString() : '');
        window.history.replaceState({}, document.title, newUrl);
      }
    });
  }
  
  loadRegistrationSettings(): void {
    console.log('loadRegistrationSettings called');
    this.loadingSettings = true;
    this.http.get<any>('/api/registration-settings').subscribe({
      next: (settings) => {
        console.log('Registration settings received:', settings);
        this.isInviteOnlyMode = !settings.publicRegistrationEnabled;
        this.emailVerificationRequired = settings.emailVerificationRequired || false;
        this.loadingSettings = false;
        
        console.log('Registration mode loaded - publicRegistrationEnabled:', settings.publicRegistrationEnabled, 'isInviteOnlyMode:', this.isInviteOnlyMode, 'emailVerificationRequired:', this.emailVerificationRequired);
        
        // Only load public info if NOT in invite-only mode
        if (!this.isInviteOnlyMode) {
          console.log('Loading public info because NOT invite-only');
          this.loadPublicRegistrationInfo();
          this.loadAvailableChannels();
        } else {
          console.log('Skipping public info load - invite-only mode');
        }
        
        // In public mode, code is optional
        // In invite-only mode with invitation link, code is already set
        // In invite-only mode without invitation link, code will be validated separately
        const accessCodeControl = this.registrationForm.get('accessCode');
        if (!this.isInviteOnlyMode) {
          accessCodeControl?.clearValidators();
        }
        accessCodeControl?.updateValueAndValidity();
      },
      error: (error) => {
        console.error('Error loading registration settings:', error);
        // Default to invite-only if we can't load settings
        this.isInviteOnlyMode = true;
        this.emailVerificationRequired = false;
        this.loadingSettings = false;
      }
    });
  }

  validateAccessCode(): void {
    if (!this.pendingAccessCode || this.validatingCode) {
      return;
    }

    this.validatingCode = true;
    this.codeValidationError = '';

    // Try to validate as both invitation code and registration code
    this.http.post<any>('/api/user/validate-access-code', {
      code: this.pendingAccessCode
    }).subscribe({
      next: (response) => {
        this.validatingCode = false;
        if (response.valid) {
          this.codeValidated = true;
          this.validatedGroupInfo = response.groupInfo;
          
          // Set the code in the form
          this.registrationForm.patchValue({
            accessCode: this.pendingAccessCode
          });
          
          // If email was provided in invitation, pre-fill it
          if (response.email) {
            this.registrationForm.patchValue({
              email: response.email
            });
          }
          
          this.snackBar.open('Code validated successfully!', 'Close', {
            duration: 3000,
            panelClass: ['success-snackbar']
          });
        } else {
          this.codeValidationError = response.message || 'Invalid code';
        }
      },
      error: (error) => {
        this.validatingCode = false;
        this.codeValidationError = error.error?.message || error.error?.error || 'Invalid or expired code';
      }
    });
  }
  
  handleInvitation(inviteCode: string): void {
    console.log('handleInvitation called with code:', inviteCode);
    // Validate invitation code
    this.http.get(`/api/user/validate-invitation?code=${inviteCode}`).subscribe({
      next: (response: any) => {
        console.log('Invitation validation response:', response);
        if (response.valid) {
          // Pre-fill email if provided in invitation
          if (response.email) {
            this.registrationForm.patchValue({ email: response.email });
          }
          
          // Set invitation code as accessCode (hidden field)
          this.registrationForm.patchValue({ accessCode: inviteCode });
          this.invitationCode = inviteCode;
          this.invitationGroupName = response.groupName;
          this.codeValidated = true; // Mark as validated so form shows
          
          console.log('Invitation validated - codeValidated:', this.codeValidated, 'invitationCode:', this.invitationCode, 'invitationGroupName:', this.invitationGroupName);
          
          // Show success message
          this.snackBar.open(`You've been invited to join ${response.groupName}! Please complete your registration.`, 'Close', {
            duration: 5000,
            panelClass: ['success-snackbar']
          });
        }
      },
      error: (error) => {
        console.error('Invitation validation error:', error);
        this.snackBar.open(error.error?.message || 'Invalid or expired invitation', 'Close', {
          duration: 5000,
          panelClass: ['error-snackbar']
        });
      }
    });
  }

  loadPublicRegistrationInfo(): void {
    this.loadingGroupInfo = true;
    this.http.get<any>('/api/public-registration-info').subscribe({
      next: (info) => {
        this.publicGroupInfo = info;
        this.loadingGroupInfo = false;
      },
      error: (error) => {
        console.error('Error loading public registration info:', error);
        this.loadingGroupInfo = false;
      }
    });
  }

  loadAvailableChannels(): void {
    this.loadingChannels = true;
    this.http.get<any>('/api/public-registration-channels').subscribe({
      next: (response) => {
        this.availableChannels = response.systems || [];
        this.loadingChannels = false;
      },
      error: (error) => {
        console.error('Error loading available channels:', error);
        this.loadingChannels = false;
      }
    });
  }

  toggleChannels(): void {
    this.showChannels = !this.showChannels;
  }

  getTalkgroupsByTag(talkgroups: any[]): Array<{tag: string, talkgroups: any[]}> {
    const grouped: {[key: string]: any[]} = {};
    const noTag: any[] = [];

    talkgroups.forEach(tg => {
      const tag = tg.tag || '';
      if (tag) {
        if (!grouped[tag]) {
          grouped[tag] = [];
        }
        grouped[tag].push(tg);
      } else {
        noTag.push(tg);
      }
    });

    const result: Array<{tag: string, talkgroups: any[]}> = [];
    
    // Sort tags and add grouped talkgroups
    const sortedTags = Object.keys(grouped).sort();
    sortedTags.forEach(tag => {
      result.push({
        tag: tag,
        talkgroups: grouped[tag].sort((a, b) => (a.label || '').localeCompare(b.label || ''))
      });
    });

    // Add talkgroups without tags at the end
    if (noTag.length > 0) {
      result.push({
        tag: '',
        talkgroups: noTag.sort((a, b) => (a.label || '').localeCompare(b.label || ''))
      });
    }

    return result;
  }

  passwordMatchValidator(form: FormGroup) {
    const password = form.get('password');
    const confirmPassword = form.get('confirmPassword');
    
    if (password && confirmPassword && password.value !== confirmPassword.value) {
      confirmPassword.setErrors({ passwordMismatch: true });
      return { passwordMismatch: true };
    }
    
    if (confirmPassword && confirmPassword.hasError('passwordMismatch')) {
      confirmPassword.setErrors(null);
    }
    
    return null;
  }

  passwordStrengthValidator(control: any) {
    if (!control || !control.value) {
      return null;
    }
    
    const password = control.value;
    const errors: any = {};
    
    if (!/[A-Z]/.test(password)) {
      errors.requireUpper = true;
    }
    if (!/[a-z]/.test(password)) {
      errors.requireLower = true;
    }
    if (!/[0-9]/.test(password)) {
      errors.requireNumber = true;
    }
    // Check for special character
    if (!/[!@#$%^&*()_+\-=\[\]{};':"\\|,.<>\/?]/.test(password)) {
      errors.requireSpecial = true;
    }
    
    return Object.keys(errors).length > 0 ? errors : null;
  }

  // Request email verification code (Step 1 of two-step signup)
  requestEmailVerification(): void {
    const email = this.registrationForm.get('email')?.value;
    if (!email || this.registrationForm.get('email')?.invalid) {
      this.error = 'Please enter a valid email address';
      return;
    }

    this.loading = true;
    this.error = '';
    this.emailVerificationError = '';

    this.http.post('/api/user/request-signup-verification', { email: email.toLowerCase() }).subscribe({
      next: (response: any) => {
        this.loading = false;
        this.emailVerificationStep = true;
        this.pendingEmail = email.toLowerCase();
        this.snackBar.open('Verification code sent! Check your email.', 'Close', {
          duration: 5000,
          panelClass: ['success-snackbar']
        });
      },
      error: (error) => {
        this.loading = false;
        if (error.error?.error && typeof error.error.error === 'string') {
          this.error = error.error.error;
        } else if (error.error?.message && typeof error.error.message === 'string') {
          this.error = error.error.message;
        } else {
          this.error = 'Failed to send verification code. Please try again.';
        }
      }
    });
  }

  // Verify code and proceed to full registration (Step 2)
  verifyCodeAndContinue(): void {
    if (!this.verificationCode || this.verificationCode.length !== 6) {
      this.emailVerificationError = 'Please enter the 6-digit code';
      return;
    }

    this.verifyingEmail = true;
    this.emailVerificationError = '';

    // Just validate the format locally, actual verification happens during registration
    this.verifyingEmail = false;
    this.emailVerificationStep = false;
    
    // Pre-fill the email in the form
    this.registrationForm.patchValue({
      email: this.pendingEmail
    });
    
    this.snackBar.open('Code verified! Complete your registration.', 'Close', {
      duration: 3000,
      panelClass: ['success-snackbar']
    });
  }

  // Go back to email entry
  backToEmailEntry(): void {
    this.emailVerificationStep = false;
    this.verificationCode = '';
    this.emailVerificationError = '';
    this.pendingEmail = '';
  }

  onSubmit(): void {
    if (this.registrationForm.valid && !this.loading) {
      this.loading = true;
      this.error = '';

      const formData = {
        email: this.registrationForm.value.email.toLowerCase(), // Ensure email is lowercase
        password: this.registrationForm.value.password,
        firstName: this.registrationForm.value.firstName,
        lastName: this.registrationForm.value.lastName,
        zipCode: this.registrationForm.value.zipCode
      } as any;
      
      // Include verification code if email verification is required
      if (this.emailVerificationRequired && this.verificationCode) {
        formData.verificationCode = this.verificationCode;
      }
      
      // Include accessCode if provided (unified field for invitation and registration codes)
      if (this.registrationForm.value.accessCode && this.registrationForm.value.accessCode.trim() !== '') {
        formData.accessCode = this.registrationForm.value.accessCode;
      }
      
      this.http.post('/api/user/register', formData).subscribe({
        next: (response: any) => {
          this.loading = false;
          this.success = true;
          const pin = response?.pin;
          if (typeof pin === 'string' && pin.length > 0) {
            this.generatedPin = pin;
            this.rdioScannerService.savePin(pin);
          } else {
            this.generatedPin = null;
          }
        },
        error: (error) => {
          this.loading = false;
          // Display backend validation errors
          if (error.error?.error && typeof error.error.error === 'string') {
            this.error = error.error.error;
          } else if (error.error?.message && typeof error.error.message === 'string') {
            this.error = error.error.message;
          } else if (typeof error.error === 'string') {
            this.error = error.error;
          } else {
            this.error = 'Registration failed. Please try again.';
          }
        }
      });
    }
  }

  switchToLogin(): void {
    // Clear any stored PIN to ensure user must log in
    this.rdioScannerService.clearPin();
    // Force a full page reload to ensure clean state
    window.location.href = '/';
  }

  resendVerification(): void {
    if (this.registrationForm.get('email')?.value && !this.loading) {
      this.loading = true;
      this.error = '';

      this.http.post('/api/user/resend-verification', {
        email: this.registrationForm.get('email')?.value
      }).subscribe({
        next: (response: any) => {
          this.loading = false;
          this.error = '';
          // Show success message
        },
        error: (error) => {
          this.loading = false;
          this.error = error.error || 'Failed to resend verification email.';
        }
      });
    }
  }
  
  getResendCooldownText(): string {
    if (this.resendCooldown <= 0) {
      return '';
    }
    const minutes = Math.floor(this.resendCooldown / 60);
    const seconds = this.resendCooldown % 60;
    return minutes > 0 ? `${minutes}m ${seconds}s` : `${seconds}s`;
  }
}
