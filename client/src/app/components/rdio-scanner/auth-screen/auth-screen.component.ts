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

import { Component, OnInit, Output, EventEmitter, OnDestroy, AfterViewChecked, AfterViewInit, ChangeDetectorRef, NgZone } from '@angular/core';
import { FormBuilder, FormGroup, Validators } from '@angular/forms';
import { HttpClient } from '@angular/common/http';
import { Router, ActivatedRoute } from '@angular/router';
import { MatSnackBar } from '@angular/material/snack-bar';
import { MatDialog } from '@angular/material/dialog';
import packageInfo from '../../../../../package.json';
import { RdioScannerService } from '../rdio-scanner.service';
import { RdioScannerEvent, RdioScannerConfig } from '../rdio-scanner';
import { Subscription } from 'rxjs';

@Component({
    selector: 'rdio-scanner-auth-screen',
    templateUrl: './auth-screen.component.html',
    styleUrls: ['./auth-screen.component.scss'],
    standalone: false
})
export class RdioScannerAuthScreenComponent implements OnInit, OnDestroy, AfterViewChecked, AfterViewInit {
  @Output() authenticated = new EventEmitter<void>();

  authMode: 'login' | 'register' | 'group-admin' = 'login';
  loginForm: FormGroup;
  registerForm: FormGroup;
  groupAdminForm: FormGroup;
  forgotPasswordForm: FormGroup;
  resetPasswordForm: FormGroup;
  loading = false;
  groupAdminLoading = false;
  error = '';
  groupAdminError = '';
  success = false;
  successMessage = '';
  baseUrl = '';
  config: RdioScannerConfig | undefined;
  showCheckout = false;
  showCheckoutSuccess = false;
  showCheckoutCancel = false;
  showForgotPassword = false;
  showResetPassword = false;
  resetEmail = '';
  
  // Logo URL - set once to prevent spam
  logoUrl: string = '';
  logoError = false;
  
  // Public registration info
  publicGroupInfo: any = null;
  loadingGroupInfo = false;
  availableChannels: any[] = [];
  loadingChannels = false;
  showChannels = false;

  // Multi-tier public registration: every group flagged public is a tier the
  // user can join. Paid tiers are billed together on one combined subscription.
  tiers: any[] = [];
  selectedTiers: { [groupId: number]: boolean } = {};
  tierPrice: { [groupId: number]: string } = {};
  primaryTierId: number | null = null;
  /** Set from `/api/registration-settings` after load — do not assume invite-only before then (fixes invite box vanishing when public mode loads). */
  registrationSettingsLoaded = false;
  isInviteOnlyMode = true;
  codeValidated = false;
  pendingAccessCode = '';
  validatingCode = false;
  codeValidationError = '';

  /** When true, signup is email → 6-digit code → full form (from `/api/registration-settings`). */
  emailVerificationRequired = false;
  emailVerificationStep = false;
  verificationCode = '';
  verifyingEmail = false;
  emailVerificationError = '';
  pendingEmail = '';
  /** True after user enters the 6-digit signup code (email-verification-required flow only). */
  signupEmailCodeConfirmed = false;
  /**
   * When true, the registration email input is read-only (OTP confirmed, or invite
   * bound to a specific email). Access/invite codes without a bound email must NOT
   * set this — that left an empty "Verified" field users could not type into (#261).
   */
  emailLockedAfterVerify = false;
  /** True when registration completed via an access/invitation code — user is auto-verified, no email check needed. */
  registeredWithCode = false;
  
  private connectionLimitAlertShown = false;
  private eventSubscription: Subscription | undefined;
  private waitingForSubscriptionCheck = false;
  
  // Resend verification email rate limiting
  resendDisabled = false;
  private resendCooldown = 0;
  private resendInterval: any = null;
  
  // Countdown for blocked logins
  isBlocked = false;
  countdownSeconds = 0;
  private countdownInterval: any;
  
  // Turnstile CAPTCHA — tokens are single-use; never persist across attempts (issue #239).
  turnstileToken = '';
  turnstileWidgetId: any = null;
  turnstileSiteKey: string = '';
  turnstileEnabled: boolean = false;
  private turnstileInitAttempted = false;
  /** True while the Turnstile script/widget is being set up (shown as placeholder). */
  turnstileInitializing = false;

  /** True when CAPTCHA is required but the widget has not finished rendering yet. */
  get turnstilePending(): boolean {
    return !!(this.turnstileEnabled && this.turnstileSiteKey && !this.turnstileToken && this.turnstileWidgetId === null);
  }

  constructor(
    private fb: FormBuilder,
    private http: HttpClient,
    private router: Router,
    private route: ActivatedRoute,
    private snackBar: MatSnackBar,
    private dialog: MatDialog,
    private rdioScannerService: RdioScannerService,
    private cdr: ChangeDetectorRef,
    private ngZone: NgZone
  ) {
    // Initialize login form
    this.loginForm = this.fb.group({
      email: ['', [Validators.required, Validators.email]],
      password: ['', [Validators.required]]
    });

    this.registerForm = this.fb.group({
      firstName: ['', [Validators.required]],
      lastName: ['', [Validators.required]],
      zipCode: ['', [Validators.required, Validators.pattern(/^\d{5}(-\d{4})?$/)]],
      email: ['', [Validators.required, Validators.email]],
      password: ['', [Validators.required, Validators.minLength(8), this.passwordStrengthValidator]],
      confirmPassword: ['', [Validators.required]],
      accessCode: ['']  // Unified field for invitation and registration codes
    }, { validators: this.passwordMatchValidator });

    this.groupAdminForm = this.fb.group({
      email: ['', [Validators.required, Validators.email]],
      password: ['', [Validators.required]]
    });

    this.forgotPasswordForm = this.fb.group({
      email: ['', [Validators.required, Validators.email]]
    });

    this.resetPasswordForm = this.fb.group({
      code: ['', [Validators.required, Validators.pattern(/^\d{6}$/)]],
      newPassword: ['', [Validators.required, Validators.minLength(8), this.passwordStrengthValidator]],
      confirmPassword: ['', [Validators.required]]
    }, { validators: this.passwordMatchValidator });
    
    // Automatically convert email to lowercase as user types
    this.loginForm.get('email')?.valueChanges.subscribe(value => {
      if (value && value !== value.toLowerCase()) {
        this.loginForm.get('email')?.setValue(value.toLowerCase(), { emitEvent: false });
      }
    });
    
    this.registerForm.get('email')?.valueChanges.subscribe(value => {
      if (value && value !== value.toLowerCase()) {
        this.registerForm.get('email')?.setValue(value.toLowerCase(), { emitEvent: false });
      }
    });
    
    this.groupAdminForm.get('email')?.valueChanges.subscribe(value => {
      if (value && value !== value.toLowerCase()) {
        this.groupAdminForm.get('email')?.setValue(value.toLowerCase(), { emitEvent: false });
      }
    });
    
    this.forgotPasswordForm.get('email')?.valueChanges.subscribe(value => {
      if (value && value !== value.toLowerCase()) {
        this.forgotPasswordForm.get('email')?.setValue(value.toLowerCase(), { emitEvent: false });
      }
    });
  }

  private loadInitialConfig(initialConfig: any): void {
    this.config = {
      branding: initialConfig.branding,
      email: initialConfig.email,
      options: initialConfig.options || {}
    } as RdioScannerConfig;
    this.baseUrl = initialConfig.options?.baseUrl || window.location.origin;
    this.turnstileEnabled = initialConfig.options?.turnstileEnabled || false;
    this.turnstileSiteKey = initialConfig.options?.turnstileSiteKey || '';
    
    // Drop any leftover single-use token from older builds (issue #239).
    try { sessionStorage.removeItem('turnstile_token'); } catch { /* ignore */ }

    // Load Turnstile if enabled
    if (this.turnstileEnabled && this.turnstileSiteKey) {
      this.loadTurnstileScript();
    }
  }

  ngOnInit(): void {
    // Check if user is blocked (from query params)
    this.route.queryParams.subscribe(params => {
      const seconds = params['seconds'];
      if (seconds && !isNaN(seconds)) {
        this.startCountdown(parseInt(seconds, 10));
      }
    });
    
    // Load registration settings first to determine if invite-only
    this.loadRegistrationSettings();
    
    // Check for Stripe checkout success/cancel parameters
    const urlParams = new URLSearchParams(window.location.search);
    const checkoutStatus = urlParams.get('checkout');
    
    if (checkoutStatus === 'success') {
      this.showCheckoutSuccess = true;
      // Clear the URL parameters
      window.history.replaceState({}, document.title, window.location.pathname);
    } else if (checkoutStatus === 'cancel') {
      this.showCheckoutCancel = true;
      // Clear the URL parameters
      window.history.replaceState({}, document.title, window.location.pathname);
    }

    // Check for invitation code in URL or sessionStorage (captured before Angular loaded)
    let inviteCode = urlParams.get('invite');
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
      urlParams.delete('invite');
      const newUrl = window.location.pathname + (urlParams.toString() ? '?' + urlParams.toString() : '');
      window.history.replaceState({}, document.title, newUrl);
    }

    // Set logo URL once to prevent spam
    this.logoUrl = `${window.location.origin}/email-logo?t=${Date.now()}`;
    
    // Check for initial config injected by server
    // The script should be in the HTML before Angular loads, but check multiple times
    let initialConfig = (window as any).initialConfig;
    
    // Check immediately
    if (initialConfig) {
      this.loadInitialConfig(initialConfig);
    } else {
      // Wait a bit and check again (in case script executes after ngOnInit)
      let checkCount = 0;
      const maxChecks = 10;
      const checkInterval = setInterval(() => {
        checkCount++;
        initialConfig = (window as any).initialConfig;
        if (initialConfig) {
          clearInterval(checkInterval);
          this.loadInitialConfig(initialConfig);
        } else if (checkCount >= maxChecks) {
          clearInterval(checkInterval);
          // Fallback: Get current config from service (in case websocket hasn't connected yet)
          const currentConfig = this.rdioScannerService.getConfig();
          if (currentConfig) {
            this.config = currentConfig;
            this.baseUrl = currentConfig.options?.baseUrl || window.location.origin;
            this.turnstileEnabled = currentConfig.options?.turnstileEnabled || false;
            this.turnstileSiteKey = currentConfig.options?.turnstileSiteKey || '';
          }
        }
      }, 50); // Check every 50ms, up to 500ms total
    }

    // Subscribe to configuration updates and connection limit errors
    this.eventSubscription = this.rdioScannerService.event.subscribe((event: RdioScannerEvent) => {
      if ('config' in event && event.config) {
        // Preserve options if they exist in initialConfig but not in WebSocket config
        if (!event.config.options && this.config?.options) {
          event.config.options = this.config.options;
        }
        this.config = event.config;
        this.baseUrl = event.config.options?.baseUrl || window.location.origin;
        
        // If we're waiting for subscription check, check it now with updated config
        if (this.waitingForSubscriptionCheck) {
          this.waitingForSubscriptionCheck = false;
          console.log('Config updated after login, checking subscription with new config:', this.config);
          this.handleSubscriptionRequired();
        }
      }
      
      // Handle connection limit exceeded
      if (event.auth && event.tooMany && !this.connectionLimitAlertShown) {
        // Connection limit exceeded - show alert only once
        this.connectionLimitAlertShown = true;
        
        const limit = event.connectionLimit || 0;
        const limitText = limit > 0 ? `Your connection limit is ${limit}.` : '';
        const message = `You have reached your connection limit. ${limitText}\n\nPlease close any other active sessions, reload this page, and try logging in again.`;
        
        // Show browser alert
        alert(message);
        
        this.error = `Connection limit reached. ${limitText} Please close other sessions and try again.`;
        this.loading = false;
        this.groupAdminLoading = false;
        
        // Reset the flag after 5 seconds in case they want to try again
        setTimeout(() => {
          this.connectionLimitAlertShown = false;
        }, 5000);
      }
    });
  }

  handleInvitation(inviteCode: string): void {
    console.log('Handling invitation code:', inviteCode);
    
    // Validate invitation code
    this.http.get(`/api/user/validate-invitation?code=${inviteCode}`).subscribe({
      next: (response: any) => {
        console.log('Invitation validation response:', response);
        if (response.valid) {
          // Switch to registration tab
          this.authMode = 'register';
          
          // Mark code as validated so form shows
          this.codeValidated = true;
          // Invitation link counts as email verification — skip the email code step
          this.signupEmailCodeConfirmed = true;
          
          // Pre-fill email if provided in invitation; only lock when we have one
          if (response.email) {
            this.registerForm.patchValue({ email: response.email });
            this.pendingEmail = response.email;
            this.emailLockedAfterVerify = true;
          } else {
            this.emailLockedAfterVerify = false;
          }
          
          // Set invitation code as accessCode
          this.registerForm.patchValue({ accessCode: inviteCode });
          console.log('Invitation code set in form as accessCode:', this.registerForm.get('accessCode')?.value);
          
          // Show success message
          this.snackBar.open(`You've been invited to join ${response.groupName}! Please complete your registration.`, 'Close', {
            duration: 5000,
            panelClass: ['success-snackbar']
          });
        }
      },
      error: (error) => {
        console.error('Invitation validation error:', error);
        this.snackBar.open(error.error?.message || error.error?.error || 'Invalid or expired invitation', 'Close', {
          duration: 5000,
          panelClass: ['error-snackbar']
        });
      }
    });
  }

  ngOnDestroy(): void {
    if (this.eventSubscription) {
      this.eventSubscription.unsubscribe();
    }
    if (this.countdownInterval) {
      clearInterval(this.countdownInterval);
    }
  }
  
  startCountdown(seconds: number): void {
    this.isBlocked = true;
    this.countdownSeconds = seconds;
    this.loading = true;
    this.groupAdminLoading = true;
    
    this.countdownInterval = setInterval(() => {
      this.countdownSeconds--;
      if (this.countdownSeconds <= 0) {
        clearInterval(this.countdownInterval);
        this.isBlocked = false;
        this.loading = false;
        this.groupAdminLoading = false;
        // Clear query params
        this.router.navigate([], {
          relativeTo: this.route,
          queryParams: {},
          queryParamsHandling: 'merge'
        });
      }
    }, 1000);
  }
  
  getCountdownDisplay(): string {
    const minutes = Math.floor(this.countdownSeconds / 60);
    const seconds = this.countdownSeconds % 60;
    return `${minutes}:${seconds.toString().padStart(2, '0')}`;
  }

  passwordMatchValidator(form: FormGroup) {
    const password = form.get('password') || form.get('newPassword');
    const confirmPassword = form.get('confirmPassword');
    
    if (password && confirmPassword && password.value !== confirmPassword.value) {
      confirmPassword.setErrors({ passwordMismatch: true });
    } else {
      if (confirmPassword?.hasError('passwordMismatch')) {
        confirmPassword.setErrors(null);
      }
    }
    
    return null;
  }

  passwordStrengthValidator(control: any) {
    if (!control || !control.value) {
      return null;
    }
    
    const password = control.value;
    const errors: any = {};
    
    // Check for uppercase letter
    if (!/[A-Z]/.test(password)) {
      errors.requireUpper = true;
    }
    
    // Check for lowercase letter
    if (!/[a-z]/.test(password)) {
      errors.requireLower = true;
    }
    
    // Check for number
    if (!/[0-9]/.test(password)) {
      errors.requireNumber = true;
    }
    
    // Check for special character
    if (!/[!@#$%^&*()_+\-=\[\]{};':"\\|,.<>\/?]/.test(password)) {
      errors.requireSpecial = true;
    }
    
    return Object.keys(errors).length > 0 ? errors : null;
  }

  setAuthMode(mode: 'login' | 'register' | 'group-admin'): void {
    // Prevent switching to register mode if registration is disabled
    if (mode === 'register' && !this.isUserRegistrationEnabled()) {
      this.authMode = 'login';
      return;
    }
    
    // If we're already in this mode, don't reset anything
    if (this.authMode === mode) {
      return;
    }
    
    this.authMode = mode;
    if (mode === 'register') {
      this.emailVerificationStep = false;
      this.verificationCode = '';
      this.pendingEmail = '';
      this.emailVerificationError = '';
      this.signupEmailCodeConfirmed = false;
      this.emailLockedAfterVerify = false;
    }
    // Clear errors when switching modes
    if (mode !== 'group-admin') {
      this.groupAdminError = '';
    }
    if (mode !== 'login') {
      this.error = '';
    }
    // Reset forgot password state when switching modes
    this.showForgotPassword = false;
    this.showResetPassword = false;
    this.resetEmail = '';
    
    // New mode gets a fresh Turnstile challenge (tokens are single-use).
    this.refreshTurnstile();
  }

  onForgotPassword(): void {
    this.showForgotPassword = true;
    this.showResetPassword = false;
    this.error = '';
  }

  onRequestReset(): void {
    if (this.forgotPasswordForm.valid && !this.loading) {
      this.loading = true;
      this.error = '';

      const formData = {
        email: this.forgotPasswordForm.value.email.toLowerCase() // Ensure email is lowercase
      };
      
      this.http.post('/api/user/forgot-password', formData).subscribe({
        next: (response: any) => {
          this.loading = false;
          this.resetEmail = formData.email;
          this.showForgotPassword = false;
          this.showResetPassword = true;
          this.error = '';
        },
        error: (error) => {
          this.loading = false;
          this.error = error.error?.error || 'Failed to send reset code. Please try again.';
        }
      });
    }
  }

  onResetPassword(): void {
    if (this.resetPasswordForm.valid && !this.loading) {
      this.loading = true;
      this.error = '';

      const formData = {
        email: this.resetEmail,
        code: this.resetPasswordForm.get('code')?.value,
        newPassword: this.resetPasswordForm.get('newPassword')?.value
      };
      
      this.http.post('/api/user/reset-password', formData).subscribe({
        next: (response: any) => {
          this.loading = false;
          // Reset forms and show login
          this.showForgotPassword = false;
          this.showResetPassword = false;
          this.resetEmail = '';
          this.forgotPasswordForm.reset();
          this.resetPasswordForm.reset();
          this.error = '';
          this.snackBar.open('Password reset successful! Please login with your new password.', 'Close', {
            duration: 5000,
            panelClass: ['success-snackbar']
          });
        },
        error: (error) => {
          this.loading = false;
          this.error = error.error?.error || 'Failed to reset password. Please check your code and try again.';
        }
      });
    }
  }

  backToLogin(): void {
    this.showForgotPassword = false;
    this.showResetPassword = false;
    this.resetEmail = '';
    this.forgotPasswordForm.reset();
    this.resetPasswordForm.reset();
    this.error = '';
  }

  onGroupAdminLogin(): void {
    if (this.groupAdminForm.valid && !this.groupAdminLoading) {
      // Check Turnstile if enabled
      if (this.turnstileEnabled && !this.turnstileToken) {
        this.groupAdminError = 'Please complete the CAPTCHA verification';
        return;
      }
      
      this.groupAdminLoading = true;
      this.groupAdminError = '';

      const formData: any = {
        email: this.groupAdminForm.value.email.toLowerCase(), // Ensure email is lowercase
        password: this.groupAdminForm.value.password
      };
      
      // Add Turnstile token if enabled, then clear immediately (single-use).
      if (this.turnstileEnabled && this.turnstileToken) {
        formData.turnstile_token = this.turnstileToken;
        this.turnstileToken = '';
      }

      this.http.post('/api/group-admin/login', formData).subscribe({
        next: (response: any) => {
          this.groupAdminLoading = false;
          this.snackBar.open('Login successful!', 'Close', {
            duration: 3000,
            panelClass: ['success-snackbar']
          });
          // Store user info in sessionStorage
          if (response.user && response.group) {
            sessionStorage.setItem('groupAdminUser', JSON.stringify(response.user));
            sessionStorage.setItem('groupAdminGroup', JSON.stringify(response.group));
            // Store PIN for authentication
            if (response.user.pin) {
              localStorage.setItem('groupAdminPin', response.user.pin);
            }
          }
          // Navigate to group admin panel
          this.router.navigate(['/group-admin']);
        },
        error: (error) => {
          this.groupAdminLoading = false;
          this.refreshTurnstile();
          // Check if IP is blocked due to too many failed attempts
          if (error.error?.blocked && error.error?.retryAfter) {
            // Navigate with query params to show countdown
            this.router.navigate([], {
              relativeTo: this.route,
              queryParams: { seconds: error.error.retryAfter },
              queryParamsHandling: 'merge'
            });
            this.startCountdown(error.error.retryAfter);
            return;
          }
          // Extract error message string
          if (typeof error.error === 'string') {
            this.groupAdminError = error.error;
          } else if (error.error?.message && typeof error.error.message === 'string') {
            this.groupAdminError = error.error.message;
          } else if (error.error?.error && typeof error.error.error === 'string') {
            this.groupAdminError = error.error.error;
          } else {
            this.groupAdminError = 'Login failed. Please check your credentials.';
          }
          this.snackBar.open(this.groupAdminError, 'Close', {
            duration: 5000,
            panelClass: ['error-snackbar']
          });
        }
      });
    }
  }

  onLogin(): void {
    if (!this.loading) {
      // Check Turnstile if enabled (but don't disable button, just show error)
      if (this.turnstileEnabled && !this.turnstileToken) {
        this.error = 'Please complete the CAPTCHA verification';
        return;
      }
      
      this.loading = true;
      this.error = '';

      const formData: any = {
        email: this.loginForm.value.email.toLowerCase(), // Ensure email is lowercase
        password: this.loginForm.value.password
      };
      
      // Add Turnstile token if enabled, then clear immediately (single-use).
      if (this.turnstileEnabled && this.turnstileToken) {
        formData.turnstile_token = this.turnstileToken;
        this.turnstileToken = '';
      }

      this.http.post('/api/user/login', formData).subscribe({
        next: (response: any) => {
          this.loading = false;
          const pin = response?.user?.pin;
          if (typeof pin === 'string' && pin.length > 0) {
            this.rdioScannerService.savePin(pin);
          }
          // Persist system admin flag so the main UI can show the Admin Panel button
          this.rdioScannerService.saveIsSystemAdmin(response?.user?.systemAdmin === true);
          console.log('Login successful:', response);
          
          // Reload the page to ensure fresh state and WebSocket connection
          window.location.reload();
        },
        error: (error) => {
          this.loading = false;
          this.refreshTurnstile();
          
          // Check if IP is blocked due to too many failed attempts
          // MUST check FIRST before any error message processing
          if (error.error?.blocked && error.error?.retryAfter) {
            // Navigate with query params to show countdown
            this.router.navigate([], {
              relativeTo: this.route,
              queryParams: { seconds: error.error.retryAfter },
              queryParamsHandling: 'merge'
            });
            this.startCountdown(error.error.retryAfter);
            return;
          }
          
          // Extract error message from different possible locations
          let errorMessage = 'Login failed. Please check your credentials.';
          
          if (error.error?.message && typeof error.error.message === 'string') {
            errorMessage = error.error.message;
          } else if (error.error?.error && typeof error.error.error === 'string') {
            errorMessage = error.error.error;
          } else if (typeof error.error === 'string') {
            errorMessage = error.error;
          } else if (error.message && typeof error.message === 'string') {
            errorMessage = error.message;
          }
          
          console.log('Login error details:', error);
          console.log('Extracted error message:', errorMessage);
          
          // Check if this is a subscription required error
          if (typeof errorMessage === 'string' && errorMessage.includes('Active subscription required')) {
            this.handleSubscriptionRequired();
            return;
          }
          
          this.error = errorMessage;
        }
      });
    }
  }


  async onRegister(): Promise<void> {
    // Multi-tier signup (2+ public groups): require an explicit selection + plan per paid tier.
    // Single-tier paid signup does not pick a plan here — that happens on /setup/plan.
    if (this.usingTierSelection() && !this.tierSelectionValid()) {
      const missingPricing = this.tiers.some(t => this.tierMissingPricing(t));
      this.error = missingPricing
        ? 'One of the selected paid tiers has no pricing plans configured. Please contact the site admin or choose another tier.'
        : 'Select at least one tier and choose a plan for each paid tier.';
      return;
    }
    if (this.registerForm.valid && !this.loading) {
      this.loading = true;
      this.error = '';

      const formData: any = {
        email: this.registerForm.get('email')?.value.toLowerCase(), // Ensure email is lowercase
        password: this.registerForm.get('password')?.value,
        firstName: this.registerForm.get('firstName')?.value,
        lastName: this.registerForm.get('lastName')?.value,
        zipCode: this.registerForm.get('zipCode')?.value
      };

      if (this.emailVerificationRequired && this.verificationCode && this.verificationCode.length === 6) {
        formData.verificationCode = this.verificationCode;
      }
      
      // Include accessCode if provided (unified field for invitation and registration codes)
      const accessCode = this.registerForm.get('accessCode')?.value;
      const hasAccessCode = accessCode && accessCode.trim() !== '';
      if (hasAccessCode) {
        formData.accessCode = accessCode;
      }
      
      // Multi-tier public signup: send the chosen tiers + primary so the server
      // records the full membership instead of falling back to one public group.
      const chosenTierIds = this.usingTierSelection() ? this.selectedTierIds() : [];
      if (chosenTierIds.length > 0) {
        formData.groupIds = chosenTierIds;
        formData.primaryGroupId = this.primaryTierId || chosenTierIds[0];
      }
      // Multi-tier paid selections settle in one combined Stripe checkout.
      // Single-tier paid signup uses /setup/plan after register instead.
      const paidItems = this.usingTierSelection()
        ? this.tiers
            .filter(t => this.selectedTiers[t.groupId] && t.billingEnabled && this.tierPrice[t.groupId])
            .map(t => ({ groupId: t.groupId, priceId: this.tierPrice[t.groupId] }))
        : [];

      console.log('Registration form data being sent:', formData);

      // Check Turnstile if enabled (but skip if using access code that looks like invitation - it's already validated via email)
      // Invitation codes are 16 chars and alphanumeric, registration codes are 12 chars with special chars
      const isLikelyInvitation = hasAccessCode && accessCode.length === 16 && /^[A-Z0-9]+$/.test(accessCode);
      if (this.turnstileEnabled && !this.turnstileToken && !isLikelyInvitation) {
        this.error = 'Please complete the CAPTCHA verification';
        this.loading = false;
        return;
      }
      
      // Add Turnstile token if enabled, then clear immediately (single-use).
      if (this.turnstileEnabled && this.turnstileToken) {
        formData.turnstile_token = this.turnstileToken;
        this.turnstileToken = '';
      }
      
      this.http.post('/api/user/register', formData).subscribe({
        next: async (response: any) => {
          this.loading = false;
          this.refreshTurnstile();
          const pin = response?.pin;
          if (typeof pin === 'string' && pin.length > 0) {
            this.rdioScannerService.savePin(pin);
          }
          const email = (this.registerForm.get('email')?.value || '').toLowerCase();

          // Paid tiers selected: start the combined Stripe checkout (one line
          // item per paid tier) instead of the single-plan setup page.
          if (paidItems.length > 0) {
            try {
              const baseUrl = window.location.origin;
              const res = await fetch('/api/stripe/create-checkout-session', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({
                  email,
                  items: paidItems,
                  successUrl: `${baseUrl}/?checkout=success`,
                  cancelUrl: `${baseUrl}/?checkout=cancel`,
                }),
              });
              const result = await res.json();
              if (result.checkoutUrl) {
                window.location.href = result.checkoutUrl;
                return;
              }
              this.error = result.error || 'Could not start checkout.';
            } catch (e: any) {
              this.error = e?.message || 'Could not start checkout.';
            }
            this.cdr.markForCheck();
            return;
          }

          const alreadyVerified =
            response?.verified === true || response?.message === 'User registered successfully.';
          if (alreadyVerified) {
            this.success = false;
            await this.router.navigate(['/setup/plan'], { queryParams: { email } });
            this.cdr.markForCheck();
            return;
          }
          this.registeredWithCode = hasAccessCode;
          this.success = true;
          this.successMessage = hasAccessCode
            ? 'Registration successful! You can now sign in.'
            : 'Registration successful! Please check your email to verify your account.';
        },
        error: (error) => {
          this.loading = false;
          this.refreshTurnstile();
          // Display backend validation errors
          if (error.error?.error && typeof error.error.error === 'string') {
            this.error = error.error.error;
          } else if (error.error?.message && typeof error.error.message === 'string') {
            this.error = error.error.message;
          } else {
            this.error = 'Registration failed. Please try again.';
          }
          // Also show in snackbar for visibility
          this.snackBar.open(this.error, 'Close', {
            duration: 5000,
            panelClass: ['error-snackbar']
          });
        }
      });
    }
  }

  requestSignupEmailVerification(): void {
    const email = this.registerForm.get('email')?.value;
    if (!email || this.registerForm.get('email')?.invalid) {
      this.error = 'Please enter a valid email address';
      return;
    }

    this.loading = true;
    this.error = '';
    this.emailVerificationError = '';

    this.http.post('/api/user/request-signup-verification', { email: email.toLowerCase() }).subscribe({
      next: () => {
        this.loading = false;
        this.pendingEmail = email.toLowerCase();
        this.emailVerificationStep = true;
        this.cdr.markForCheck();
        this.snackBar.open('Verification code sent. Check your email.', 'Close', {
          duration: 5000,
          panelClass: ['success-snackbar']
        });
      },
      error: (err) => {
        this.loading = false;
        if (err.error?.error && typeof err.error.error === 'string') {
          this.error = err.error.error;
        } else if (err.error?.message && typeof err.error.message === 'string') {
          this.error = err.error.message;
        } else {
          this.error = 'Failed to send verification code. Please try again.';
        }
        this.cdr.markForCheck();
      }
    });
  }

  verifySignupCodeAndContinue(): void {
    if (!this.verificationCode || this.verificationCode.length !== 6) {
      this.emailVerificationError = 'Please enter the 6-digit code';
      return;
    }
    this.emailVerificationError = '';
    this.emailVerificationStep = false;
    this.signupEmailCodeConfirmed = true;
    this.emailLockedAfterVerify = !!this.pendingEmail;
    this.registerForm.patchValue({ email: this.pendingEmail });
    this.cdr.markForCheck();
    this.snackBar.open('Code accepted. Complete your registration below.', 'Close', {
      duration: 4000,
      panelClass: ['success-snackbar']
    });
  }

  backSignupEmailEntry(): void {
    this.emailVerificationStep = false;
    this.verificationCode = '';
    this.emailVerificationError = '';
    this.pendingEmail = '';
    this.signupEmailCodeConfirmed = false;
    this.emailLockedAfterVerify = false;
    this.cdr.markForCheck();
  }

  resendVerification(): void {
    const email = this.registerForm.get('email')?.value;
    if (email) {
      this.http.post('/api/user/resend-verification', { email }).subscribe({
        next: () => {
          this.error = '';
          this.successMessage = 'Verification email sent! Please check your inbox.';
        },
        error: (error) => {
          this.error = error.error?.message || 'Failed to send verification email.';
        }
      });
    }
  }

  getBranding(): string {
    return this.config?.branding || 'ThinLine Radio';
  }

  getServerVersion(): string {
    return this.config?.version || packageInfo.version || '';
  }

  getSupportEmail(): string {
    return this.config?.email || '';
  }

  hasSupportEmail(): boolean {
    return !!(this.config?.email);
  }

  shouldShowTurnstile(): boolean {
    // Don't show Turnstile if user has an access code that looks like an invitation
    // Invitation codes are 16 chars and alphanumeric only
    const accessCode = this.registerForm.get('accessCode')?.value;
    if (accessCode && accessCode.length === 16 && /^[A-Z0-9]+$/.test(accessCode)) {
      return false; // Likely an invitation code, skip Turnstile
    }
    return true; // Show Turnstile for registration codes or no code
  }

  handleSubscriptionRequired(): void {
    const email = this.loginForm.get('email')?.value;
    const pricingOptions = this.config?.options?.pricingOptions;
    const stripePublishableKey = this.config?.options?.stripePublishableKey;
    
    console.log('handleSubscriptionRequired called');
    console.log('Email:', email);
    console.log('Config:', this.config);
    console.log('Pricing Options:', pricingOptions);
    console.log('Stripe Publishable Key:', stripePublishableKey);
    
    if (pricingOptions && pricingOptions.length > 0 && stripePublishableKey) {
      // Show embedded checkout with pricing options
      this.showCheckout = true;
    } else {
      // Show a message to contact support if configuration is missing
      console.log('Stripe configuration missing, showing support message');
      this.error = 'Active subscription required. Please contact support to set up your subscription.';
    }
  }

  onCheckoutSuccess(event: any): void {
    console.log('Checkout successful:', event);
    this.showCheckout = false;
    // Optionally redirect or show success message
    this.error = 'Subscription successful! You can now log in.';
  }

  onCheckoutError(event: any): void {
    console.log('Checkout error:', event);
    this.error = 'Checkout failed. Please try again or contact support.';
  }

  onCheckoutCancel(): void {
    console.log('Checkout cancelled');
    this.showCheckout = false;
  }


  loadRegistrationSettings(): void {
    console.log('[AUTH-SCREEN] loadRegistrationSettings called');
    this.http.get<any>('/api/registration-settings').subscribe({
      next: (settings) => {
        console.log('[AUTH-SCREEN] Registration settings received:', settings);
        this.isInviteOnlyMode = !settings.publicRegistrationEnabled;
        this.emailVerificationRequired = !!settings.emailVerificationRequired;
        console.log('[AUTH-SCREEN] isInviteOnlyMode set to:', this.isInviteOnlyMode);
        
        // Only load public info if NOT in invite-only mode
        if (!this.isInviteOnlyMode) {
          console.log('[AUTH-SCREEN] Loading public info - public mode');
          this.loadPublicRegistrationInfo();
          this.loadAvailableChannels();
        } else {
          console.log('[AUTH-SCREEN] Skipping public info - invite-only mode');
        }
        this.registrationSettingsLoaded = true;
        this.cdr.markForCheck();
      },
      error: (error) => {
        console.error('[AUTH-SCREEN] Error loading registration settings:', error);
        // Default to invite-only if we can't load settings
        this.isInviteOnlyMode = true;
        this.emailVerificationRequired = false;
        this.registrationSettingsLoaded = true;
        this.cdr.markForCheck();
      }
    });
  }

  loadPublicRegistrationInfo(): void {
    this.loadingGroupInfo = true;
    this.http.get<any>('/api/public-registration-info').subscribe({
      next: (info) => {
        this.publicGroupInfo = info;
        // Only offer self-billable public tiers for signup (admin-billed tiers
        // reject at register with "cannot be self-selected").
        const rawTiers = info?.tiers || [];
        this.tiers = rawTiers.filter((t: any) => t.selfBillable !== false);
        // A single tier behaves exactly like the old single-group signup.
        if (this.tiers.length === 1) {
          this.selectedTiers[this.tiers[0].groupId] = true;
          this.primaryTierId = this.tiers[0].groupId;
        }
        this.loadingGroupInfo = false;
        // Refresh channels now that tiers (and any auto-selected single tier) are known.
        this.loadAvailableChannels();
        this.cdr.markForCheck();
      },
      error: (error) => {
        console.error('Error loading public registration info:', error);
        this.loadingGroupInfo = false;
        this.cdr.markForCheck();
      }
    });
  }

  toggleTier(groupId: number): void {
    this.selectedTiers[groupId] = !this.selectedTiers[groupId];
    if (this.selectedTiers[groupId]) {
      if (this.primaryTierId === null) {
        this.primaryTierId = groupId;
      }
      this.autoSelectSoleTierPrice(groupId);
    } else {
      delete this.tierPrice[groupId];
      if (this.primaryTierId === groupId) {
        const remaining = this.selectedTierIds();
        this.primaryTierId = remaining.length ? remaining[0] : null;
      }
    }
    // Keep the "available channels" union in sync with the selection.
    this.loadAvailableChannels();
    this.cdr.markForCheck();
  }

  /** When a paid tier has exactly one pricing option, pick it automatically. */
  autoSelectSoleTierPrice(groupId: number): void {
    const tier = this.tiers.find(t => t.groupId === groupId);
    if (!tier?.billingEnabled || this.tierPrice[groupId]) {
      return;
    }
    const options = tier.pricingOptions || [];
    if (options.length === 1 && options[0]?.priceId) {
      this.tierPrice[groupId] = options[0].priceId;
    }
  }

  setTierPrice(groupId: number, priceId: string): void {
    this.tierPrice[groupId] = priceId;
    this.cdr.markForCheck();
  }

  /** True when a selected paid tier has billing on but no pricing options configured. */
  tierMissingPricing(tier: any): boolean {
    return !!this.selectedTiers[tier.groupId] && !!tier.billingEnabled && !(tier.pricingOptions?.length);
  }

  selectedTierIds(): number[] {
    return this.tiers.filter(t => this.selectedTiers[t.groupId]).map(t => t.groupId);
  }

  /** Every selected paid tier must have a pricing option chosen. */
  tierSelectionValid(): boolean {
    if (this.selectedTierIds().length === 0) {
      return false;
    }
    return this.tiers.every(t => {
      if (!this.selectedTiers[t.groupId]) { return true; }
      if (!t.billingEnabled) { return true; }
      // Misconfigured paid tier (no prices) cannot be joined via self-serve signup.
      if (!(t.pricingOptions?.length)) { return false; }
      return !!this.tierPrice[t.groupId];
    });
  }

  /**
   * True when the signup form should require an explicit tier selection.
   * Matches the server: only when more than one public registration group exists.
   * Single-tier paid signup continues to choose a plan on /setup/plan after register.
   */
  usingTierSelection(): boolean {
    return this.tiers.length > 1 && !this.registerForm.get('accessCode')?.value?.trim();
  }

  loadAvailableChannels(): void {
    // Show the union of channels granted by the selected tiers (or all public
    // tiers before anything is selected / single-tier signup).
    let ids = this.selectedTierIds();
    if (ids.length === 0) {
      ids = this.tiers.map(t => t.groupId);
    }

    // No tier info yet — fall back to the legacy single-group endpoint.
    if (ids.length === 0) {
      this.loadingChannels = true;
      this.http.get<any>('/api/public-registration-channels').subscribe({
        next: (response) => { this.availableChannels = response.systems || []; this.loadingChannels = false; this.cdr.markForCheck(); },
        error: () => { this.loadingChannels = false; this.cdr.markForCheck(); }
      });
      return;
    }

    this.loadingChannels = true;
    const sysMap = new Map<any, any>();
    let pending = ids.length;
    const done = () => {
      if (--pending > 0) { return; }
      this.availableChannels = [...sysMap.values()].map((e: any) => {
        const { _tgIds, ...rest } = e;
        return rest;
      });
      this.loadingChannels = false;
      this.cdr.markForCheck();
    };
    ids.forEach(id => {
      this.http.get<any>(`/api/public-registration-channels?groupId=${id}`).subscribe({
        next: (response) => {
          (response?.systems || []).forEach((sys: any) => {
            let entry = sysMap.get(sys.id);
            if (!entry) {
              entry = { id: sys.id, label: sys.label, talkgroups: [], _tgIds: new Set() };
              sysMap.set(sys.id, entry);
            }
            (sys.talkgroups || []).forEach((tg: any) => {
              if (!entry._tgIds.has(tg.id)) { entry._tgIds.add(tg.id); entry.talkgroups.push(tg); }
            });
          });
          done();
        },
        error: () => done()
      });
    });
  }

  toggleChannels(): void {
    this.showChannels = !this.showChannels;
  }

  getTotalChannelCount(): number {
    return this.availableChannels.reduce((total: number, system: any) => {
      return total + (system.talkgroups?.length || 0);
    }, 0);
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

  validateAccessCode(): void {
    if (!this.pendingAccessCode || this.validatingCode) {
      return;
    }

    this.validatingCode = true;
    this.codeValidationError = '';

    this.http.post<any>('/api/user/validate-access-code', {
      code: this.pendingAccessCode
    }).subscribe({
      next: (response) => {
        this.validatingCode = false;
        if (response.valid) {
          this.codeValidated = true;
          // A validated access/invitation code counts as email verification — skip the email code step
          this.signupEmailCodeConfirmed = true;
          
          // Set the code in the form
          this.registerForm.patchValue({
            accessCode: this.pendingAccessCode
          });
          
          // Only lock email when the code is bound to a specific address.
          // Generic access codes leave email empty and editable (#261).
          if (response.email) {
            this.registerForm.patchValue({
              email: response.email
            });
            this.pendingEmail = response.email;
            this.emailLockedAfterVerify = true;
          } else {
            this.emailLockedAfterVerify = false;
          }
          
          this.snackBar.open('Code validated successfully!', 'Close', {
            duration: 3000
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

  closeCheckoutSuccess(): void {
    this.showCheckoutSuccess = false;
  }

  closeCheckoutCancel(): void {
    this.showCheckoutCancel = false;
  }
  
  getResendCooldownText(): string {
    if (this.resendCooldown <= 0) {
      return '';
    }
    const minutes = Math.floor(this.resendCooldown / 60);
    const seconds = this.resendCooldown % 60;
    return minutes > 0 ? `${minutes}m ${seconds}s` : `${seconds}s`;
  }

  isUserRegistrationEnabled(): boolean {
    // If options are missing (e.g., websocket config without options), assume enabled
    const opt = this.config?.options;
    if (!opt || opt.userRegistrationEnabled === undefined) {
      return true;
    }
    return opt.userRegistrationEnabled === true;
  }

  getLogoUrl(): string {
    // Return the cached logo URL (set once in ngOnInit)
    return this.logoUrl;
  }

  getLogoBorderRadius(): string {
    const borderRadius = this.config?.options?.emailLogoBorderRadius;
    // Return the configured border radius, or default to '8px' for a nice rounded look
    // (matching common email styling)
    return borderRadius && borderRadius.trim() !== '' ? borderRadius : '8px';
  }

  onLogoError(event: Event): void {
    const img = event.target as HTMLImageElement;
    if (img) {
      this.logoError = true;
      img.style.display = 'none';
    }
  }
  
  loadTurnstileScript(): void {
    // Check if script is already loaded
    if ((window as any).turnstile) {
      this.initTurnstileWidget();
      return;
    }

    // Load Turnstile script (latest version)
    const script = document.createElement('script');
    script.src = 'https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit';
    script.async = true;
    script.defer = true;
    script.onload = () => {
      this.initTurnstileWidget();
    };
    document.head.appendChild(script);
  }

  initTurnstileWidget(): void {
    // Prevent multiple simultaneous initializations - set flag IMMEDIATELY
    if (this.turnstileInitAttempted || this.turnstileInitializing) {
      return;
    }
    
    this.turnstileInitAttempted = true;
    this.turnstileInitializing = true;
    this.turnstileToken = '';
    try { sessionStorage.removeItem('turnstile_token'); } catch { /* ignore */ }
    
    // Wait for DOM to be ready
    setTimeout(() => {
      const widgetContainer = document.getElementById('turnstile-widget-auth');
      if (widgetContainer && (window as any).turnstile && this.turnstileSiteKey) {
        // Remove existing widget if any
        if (this.turnstileWidgetId !== null) {
          try {
            (window as any).turnstile.remove(this.turnstileWidgetId);
          } catch (e) {
            // Ignore errors
          }
          this.turnstileWidgetId = null;
        }
        
        // Clear container
        widgetContainer.innerHTML = '';
        
        try {
          this.turnstileWidgetId = (window as any).turnstile.render(widgetContainer, {
            sitekey: this.turnstileSiteKey,
            callback: (token: string) => {
              // Wrap in ngZone to ensure Angular detects the change
              this.ngZone.run(() => {
                this.turnstileToken = token;
                this.error = ''; // Clear error when token is received
                this.cdr.detectChanges();
              });
            },
            'error-callback': (_errorCode: string) => {
              this.ngZone.run(() => {
                this.turnstileToken = '';
                this.error = 'CAPTCHA verification failed. Please try again.';
                this.cdr.detectChanges();
              });
            },
            'expired-callback': () => {
              this.ngZone.run(() => {
                this.turnstileToken = '';
                this.cdr.detectChanges();
              });
            },
            theme: 'light',
            size: 'normal'
          });
          this.turnstileInitializing = false;
          this.ngZone.run(() => this.cdr.detectChanges());
        } catch (e) {
          // If rendering fails, reset the flags so we can try again
          this.turnstileInitAttempted = false;
          this.turnstileInitializing = false;
          this.ngZone.run(() => this.cdr.detectChanges());
        }
      } else {
        this.turnstileInitializing = false;
        this.ngZone.run(() => this.cdr.detectChanges());
      }
    }, 300);
  }

  /** Clear the used token and force a new Turnstile challenge (issue #239). */
  refreshTurnstile(): void {
    if (!this.turnstileEnabled) {
      return;
    }
    this.turnstileToken = '';
    try { sessionStorage.removeItem('turnstile_token'); } catch { /* ignore */ }

    if (this.turnstileWidgetId !== null && (window as any).turnstile) {
      try {
        (window as any).turnstile.reset(this.turnstileWidgetId);
        this.cdr.detectChanges();
        return;
      } catch {
        // Fall through to full re-render
      }
    }

    this.resetTurnstile();
    this.turnstileInitAttempted = false;
    this.turnstileInitializing = false;
    setTimeout(() => this.initTurnstileWidget(), 100);
  }
  
  resetTurnstile(): void {
    if (this.turnstileWidgetId !== null && (window as any).turnstile) {
      try {
        (window as any).turnstile.remove(this.turnstileWidgetId);
      } catch (e) {
        // Ignore errors
      }
      this.turnstileWidgetId = null;
    }
    // Clear the container
    const widgetContainer = document.getElementById('turnstile-widget-auth');
    if (widgetContainer) {
      widgetContainer.innerHTML = '';
    }
    this.turnstileToken = '';
    try { sessionStorage.removeItem('turnstile_token'); } catch { /* ignore */ }
  }
  
  private autofillCheckAttempts = 0;
  private maxAutofillChecks = 20; // Check for 10 seconds (20 * 500ms)
  
  ngAfterViewInit(): void {
    // No special autofill handling needed - we read values directly from inputs on submit
  }
  
  ngAfterViewChecked(): void {
    // Check if we need to initialize Turnstile widget
    // Only initialize once per auth mode change
    if (this.turnstileEnabled && this.turnstileSiteKey && !this.turnstileInitAttempted) {
      const widgetContainer = document.getElementById('turnstile-widget-auth');
      if (widgetContainer && (window as any).turnstile && widgetContainer.children.length === 0) {
        // Only initialize if container is empty (no widget already rendered)
        this.initTurnstileWidget();
      }
    }
  }
}

