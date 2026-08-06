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

import { Component, OnInit, OnDestroy, ChangeDetectionStrategy } from '@angular/core';
import { FormBuilder, FormGroup, Validators } from '@angular/forms';
import { HttpClient } from '@angular/common/http';
import { Router, ActivatedRoute } from '@angular/router';
import { MatSnackBar } from '@angular/material/snack-bar';

@Component({
    selector: 'rdio-scanner-group-admin-login',
    templateUrl: './group-admin-login.component.html',
    styleUrls: ['./group-admin-login.component.scss'],
    changeDetection: ChangeDetectionStrategy.Eager,
    standalone: false
})
export class RdioScannerGroupAdminLoginComponent implements OnInit, OnDestroy {
  loginForm: FormGroup;
  loading = false;
  error = '';
  
  // Countdown for blocked logins
  isBlocked = false;
  countdownSeconds = 0;
  private countdownInterval: any;
  
  // Turnstile CAPTCHA
  turnstileToken: string = '';
  turnstileWidgetId: any = null;
  turnstileSiteKey: string = '';
  turnstileEnabled: boolean = false;

  constructor(
    private fb: FormBuilder,
    private http: HttpClient,
    private router: Router,
    private route: ActivatedRoute,
    private snackBar: MatSnackBar
  ) {
    this.loginForm = this.fb.group({
      email: ['', [Validators.required, Validators.email]],
      password: ['', [Validators.required]]
    });
    
    // Automatically convert email to lowercase as user types
    this.loginForm.get('email')?.valueChanges.subscribe(value => {
      if (value && value !== value.toLowerCase()) {
        this.loginForm.get('email')?.setValue(value.toLowerCase(), { emitEvent: false });
      }
    });
  }

  ngOnInit(): void {
    // Check if user is blocked (from query params)
    this.route.queryParams.subscribe(params => {
      const seconds = params['seconds'];
      if (seconds && !isNaN(seconds)) {
        this.startCountdown(parseInt(seconds, 10));
      }
    });
    
    // Get Turnstile config from server
    const initialConfig = (window as any).initialConfig;
    if (initialConfig?.options) {
      this.turnstileEnabled = initialConfig.options.turnstileEnabled || false;
      this.turnstileSiteKey = initialConfig.options.turnstileSiteKey || '';
    }
    
    // Load Turnstile if enabled
    if (this.turnstileEnabled && this.turnstileSiteKey) {
      this.loadTurnstileScript();
    }
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

  onSubmit(): void {
    if (this.loginForm.valid && !this.loading) {
      // Check Turnstile if enabled
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
      
      // Add Turnstile token if enabled, then clear immediately (single-use / issue #239).
      if (this.turnstileEnabled && this.turnstileToken) {
        formData.turnstile_token = this.turnstileToken;
        this.turnstileToken = '';
      }

      this.http.post('/api/group-admin/login', formData).subscribe({
        next: (response: any) => {
          this.loading = false;
          this.snackBar.open('Login successful!', 'Close', {
            duration: 3000,
            panelClass: ['success-snackbar']
          });
          // Store user info in session/localStorage if needed
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
          this.loading = false;
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
            this.error = error.error;
          } else if (error.error?.message && typeof error.error.message === 'string') {
            this.error = error.error.message;
          } else if (error.error?.error && typeof error.error.error === 'string') {
            this.error = error.error.error;
          } else {
            this.error = 'Login failed. Please check your credentials.';
          }
          this.snackBar.open(this.error, 'Close', {
            duration: 5000,
            panelClass: ['error-snackbar']
          });
        }
      });
    }
  }

  /** Clear used token and force a new Turnstile challenge (issue #239). */
  refreshTurnstile(): void {
    if (!this.turnstileEnabled) {
      return;
    }
    this.turnstileToken = '';
    if (this.turnstileWidgetId !== null && (window as any).turnstile) {
      try {
        (window as any).turnstile.reset(this.turnstileWidgetId);
        return;
      } catch {
        // Fall through to re-init
      }
    }
    this.initTurnstileWidget();
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
    // Wait for DOM to be ready
    setTimeout(() => {
      const widgetContainer = document.getElementById('turnstile-widget-group-admin');
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
        
        // Reset token
        this.turnstileToken = '';
        
        try {
          this.turnstileWidgetId = (window as any).turnstile.render(widgetContainer, {
            sitekey: this.turnstileSiteKey,
            callback: (token: string) => {
              this.turnstileToken = token;
              this.error = ''; // Clear error when token is received
            },
            'error-callback': () => {
              this.turnstileToken = '';
              this.error = 'CAPTCHA verification failed. Please try again.';
            },
            'expired-callback': () => {
              this.turnstileToken = '';
            },
            theme: 'light',
            size: 'normal'
          });
        } catch (e) {
          console.error('Error rendering Turnstile widget:', e);
        }
      }
    }, 300);
  }
}

