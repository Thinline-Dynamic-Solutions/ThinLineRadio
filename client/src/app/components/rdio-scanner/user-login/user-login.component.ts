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

import { Component, OnInit, OnDestroy } from '@angular/core';
import { FormBuilder, FormGroup, Validators } from '@angular/forms';
import { HttpClient } from '@angular/common/http';
import { ActivatedRoute, Router } from '@angular/router';
import { RdioScannerService } from '../rdio-scanner.service';

@Component({
  selector: 'rdio-scanner-user-login',
  templateUrl: './user-login.component.html',
  styleUrls: ['./user-login.component.scss']
})
export class RdioScannerUserLoginComponent implements OnInit, OnDestroy {
  loginForm: FormGroup;
  forgotPasswordForm: FormGroup;
  resetPasswordForm: FormGroup;
  forcedPasswordResetForm: FormGroup;
  loading = false;
  error = '';
  showForgotPassword = false;
  showResetPassword = false;
  showForcedPasswordReset = false;
  resetEmail = '';
  forcedResetEmail = '';
  forcedResetCurrentPassword = '';
  
  // Countdown for blocked logins
  isBlocked = false;
  countdownSeconds = 0;
  private countdownInterval: any;

  constructor(
    private fb: FormBuilder,
    private http: HttpClient,
    private rdioScannerService: RdioScannerService,
    private route: ActivatedRoute,
    private router: Router
  ) {
    this.loginForm = this.fb.group({
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

    this.forcedPasswordResetForm = this.fb.group({
      newPassword: ['', [Validators.required, Validators.minLength(8), this.passwordStrengthValidator]],
      confirmPassword: ['', [Validators.required]]
    }, { validators: this.passwordMatchValidator });
    
    // Automatically convert email to lowercase as user types
    this.loginForm.get('email')?.valueChanges.subscribe(value => {
      if (value && value !== value.toLowerCase()) {
        this.loginForm.get('email')?.setValue(value.toLowerCase(), { emitEvent: false });
      }
    });
    
    this.forgotPasswordForm.get('email')?.valueChanges.subscribe(value => {
      if (value && value !== value.toLowerCase()) {
        this.forgotPasswordForm.get('email')?.setValue(value.toLowerCase(), { emitEvent: false });
      }
    });
  }

  passwordMatchValidator(form: FormGroup) {
    const password = form.get('newPassword');
    const confirmPassword = form.get('confirmPassword');
    if (password && confirmPassword && password.value !== confirmPassword.value) {
      confirmPassword.setErrors({ passwordMismatch: true });
      return { passwordMismatch: true };
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

  ngOnInit(): void {
    // Check if user is blocked (from query params)
    this.route.queryParams.subscribe(params => {
      const seconds = params['seconds'];
      if (seconds && !isNaN(seconds)) {
        this.startCountdown(parseInt(seconds, 10));
      }
    });
  }
  
  ngOnDestroy(): void {
    if (this.countdownInterval) {
      clearInterval(this.countdownInterval);
    }
  }
  
  startCountdown(seconds: number): void {
    this.isBlocked = true;
    this.countdownSeconds = seconds;
    this.loading = true; // Disable form
    
    this.countdownInterval = setInterval(() => {
      this.countdownSeconds--;
      if (this.countdownSeconds <= 0) {
        clearInterval(this.countdownInterval);
        this.isBlocked = false;
        this.loading = false;
        // Clear query params and refresh
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

  onSubmit(): void {
    if (this.loginForm.valid && !this.loading) {
      this.loading = true;
      this.error = '';

      const formData = {
        email: this.loginForm.value.email.toLowerCase(), // Ensure email is lowercase
        password: this.loginForm.value.password
      };
      
      this.http.post('/api/user/login', formData).subscribe({
        next: (response: any) => {
          this.loading = false;
          if (response?.user?.needsPasswordReset === true) {
            this.showForcedPasswordReset = true;
            this.showForgotPassword = false;
            this.showResetPassword = false;
            this.forcedResetEmail = formData.email;
            this.forcedResetCurrentPassword = formData.password;
            this.error = '';
            return;
          }
          const pin = response?.user?.pin;
          if (typeof pin === 'string' && pin.length > 0) {
            this.rdioScannerService.savePin(pin);
          }
          // Persist system admin flag so the main UI can show the Admin Panel button
          this.rdioScannerService.saveIsSystemAdmin(response?.user?.systemAdmin === true);
          console.log('Login successful:', response);
        },
        error: (error) => {
          this.loading = false;
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
          if (error.error?.error && typeof error.error.error === 'string') {
            this.error = error.error.error;
          } else if (error.error?.message && typeof error.error.message === 'string') {
            this.error = error.error.message;
          } else if (typeof error.error === 'string') {
            this.error = error.error;
          } else {
            this.error = 'Login failed. Please check your credentials.';
          }
        }
      });
    }
  }

  onForcedPasswordReset(): void {
    if (this.forcedPasswordResetForm.valid && !this.loading) {
      this.loading = true;
      this.error = '';

      const newPassword = this.forcedPasswordResetForm.get('newPassword')?.value;
      const updatePayload = {
        email: this.forcedResetEmail,
        currentPassword: this.forcedResetCurrentPassword,
        newPassword
      };

      this.http.post('/api/user/force-password-reset', updatePayload).subscribe({
        next: () => {
          const loginPayload = {
            email: this.forcedResetEmail,
            password: newPassword
          };
          this.http.post('/api/user/login', loginPayload).subscribe({
            next: (response: any) => {
              this.loading = false;
              const pin = response?.user?.pin;
              if (typeof pin === 'string' && pin.length > 0) {
                this.rdioScannerService.savePin(pin);
              }
              this.rdioScannerService.saveIsSystemAdmin(response?.user?.systemAdmin === true);
              this.showForcedPasswordReset = false;
              this.forcedPasswordResetForm.reset();
              this.forcedResetEmail = '';
              this.forcedResetCurrentPassword = '';
              this.error = '';
            },
            error: () => {
              this.loading = false;
              this.error = 'Password was changed, but automatic login failed. Please sign in with your new password.';
              this.showForcedPasswordReset = false;
              this.forcedPasswordResetForm.reset();
              this.forcedResetEmail = '';
              this.forcedResetCurrentPassword = '';
            }
          });
        },
        error: (error) => {
          this.loading = false;
          if (error.error?.error && typeof error.error.error === 'string') {
            this.error = error.error.error;
          } else if (error.error?.message && typeof error.error.message === 'string') {
            this.error = error.error.message;
          } else {
            this.error = 'Failed to update password. Please try again.';
          }
        }
      });
    }
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
          alert('Password reset successful! Please login with your new password.');
        },
        error: (error) => {
          this.loading = false;
          // Display backend validation errors
          if (error.error?.error && typeof error.error.error === 'string') {
            this.error = error.error.error;
          } else if (error.error?.message && typeof error.error.message === 'string') {
            this.error = error.error.message;
          } else {
            this.error = 'Failed to reset password. Please check your code and try again.';
          }
        }
      });
    }
  }

  backToLogin(): void {
    this.showForgotPassword = false;
    this.showResetPassword = false;
    this.showForcedPasswordReset = false;
    this.resetEmail = '';
    this.forcedResetEmail = '';
    this.forcedResetCurrentPassword = '';
    this.forgotPasswordForm.reset();
    this.resetPasswordForm.reset();
    this.forcedPasswordResetForm.reset();
    this.error = '';
  }
}
