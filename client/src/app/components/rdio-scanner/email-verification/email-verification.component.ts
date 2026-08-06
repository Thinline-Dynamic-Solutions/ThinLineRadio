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
import { ActivatedRoute, Router } from '@angular/router';
import { HttpClient } from '@angular/common/http';
import { MatSnackBar } from '@angular/material/snack-bar';
import { RdioScannerService } from '../rdio-scanner.service';
import { RdioScannerEvent, RdioScannerConfig } from '../rdio-scanner';
import { Subscription } from 'rxjs';

@Component({
    selector: 'rdio-scanner-email-verification',
    templateUrl: './email-verification.component.html',
    styleUrls: ['./email-verification.component.scss'],
    standalone: false
})
export class RdioScannerEmailVerificationComponent implements OnInit, OnDestroy {
  token = '';
  loading = true;
  verified = false;
  error = '';
  message = '';
  config: RdioScannerConfig | undefined;

  private eventSubscription: Subscription | undefined;

  constructor(
    private route: ActivatedRoute,
    private router: Router,
    private http: HttpClient,
    private matSnackBar: MatSnackBar,
    private rdioScannerService: RdioScannerService
  ) {}

  ngOnInit(): void {
    // Check for initial config injected by server
    const initialConfig = (window as any).initialConfig;
    if (initialConfig) {
      this.config = {
        branding: initialConfig.branding,
        email: initialConfig.email,
        options: initialConfig.options
      } as RdioScannerConfig;
    } else {
      // Fallback: Get current config from service
      const currentConfig = this.rdioScannerService.getConfig();
      if (currentConfig) {
        this.config = currentConfig;
      }
    }

    // Subscribe to configuration updates
    this.eventSubscription = this.rdioScannerService.event.subscribe((event: RdioScannerEvent) => {
      if ('config' in event && event.config) {
        this.config = event.config;
      }
    });

    // Get token from URL parameters
    this.route.queryParams.subscribe(params => {
      this.token = params['token'] || params['verify'];
      
      if (this.token) {
        this.verifyEmail();
      } else {
        this.loading = false;
        this.error = 'No verification token provided.';
      }
    });
  }

  ngOnDestroy(): void {
    if (this.eventSubscription) {
      this.eventSubscription.unsubscribe();
    }
  }

  async verifyEmail(): Promise<void> {
    if (!this.token) {
      this.error = 'No verification token provided.';
      this.loading = false;
      return;
    }

    try {
      const response = await this.http.post('/api/user/verify', {
        token: this.token
      }).toPromise() as any;

      this.loading = false;

      this.matSnackBar.open('Email verified successfully!', 'Close', {
        duration: 4000,
        panelClass: ['success-snackbar']
      });

      const email = (response?.email || '').trim().toLowerCase();
      if (response?.requiresPlanSelection === true && email) {
        await this.router.navigate(['/setup/plan'], { queryParams: { email } });
        return;
      }
      if (email) {
        await this.router.navigate(['/setup/welcome'], { queryParams: { email } });
        return;
      }

      this.verified = true;
      this.message = 'Email verified successfully! You can now log in to your account.';
      setTimeout(() => {
        this.router.navigate(['/']);
      }, 3000);

    } catch (error: any) {
      this.loading = false;
      
      // Check if it's a "token already used" error
      if (error.status === 404 || (error.error?.message && error.error.message.includes('invalid or has expired'))) {
        // Token was already used - show success instead of error
        this.verified = true;
        this.message = 'Email already verified! You can now log in to your account.';
        
        // Show success message
        this.matSnackBar.open('Email already verified!', 'Close', {
          duration: 5000,
          panelClass: ['success-snackbar']
        });

        // Redirect to login after 3 seconds
        setTimeout(() => {
          this.router.navigate(['/']);
        }, 3000);
      } else {
        this.error = error.error?.message || 'Email verification failed.';
        
        // Show error message
        this.matSnackBar.open(this.error, 'Close', {
          duration: 5000,
          panelClass: ['error-snackbar']
        });
      }
    }
  }

  goToLogin(): void {
    // Clear any stored PIN to ensure user must log in
    this.rdioScannerService.clearPin();
    // Force a full page reload to ensure clean state
    window.location.href = '/';
  }

  resendVerification(): void {
    // This would need the user's email, which we don't have from just the token
    // For now, redirect to the main page where they can try to register again
    this.router.navigate(['/']);
  }

  getBranding(): string {
    return this.config?.branding || 'ThinLine Radio';
  }

  getSupportEmail(): string {
    return this.config?.email || '';
  }

  hasSupportEmail(): boolean {
    return !!(this.config?.email);
  }
}