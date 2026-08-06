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

import { Component, EventEmitter, Output, OnInit, OnDestroy } from '@angular/core';
import { FormBuilder, FormGroup, Validators } from '@angular/forms';
import { ActivatedRoute, Router } from '@angular/router';
import { RdioScannerAdminService } from '../admin.service';

@Component({
    selector: 'rdio-scanner-admin-login',
    styleUrls: ['./login.component.scss'],
    templateUrl: './login.component.html',
    standalone: false
})
export class RdioScannerAdminLoginComponent implements OnInit, OnDestroy {
    @Output() loggedIn = new EventEmitter<void>();

    form: FormGroup;

    message = '';
    ssoMessage = '';
    ssoLoading = false;

    /** True when the server has disabled password-based admin login */
    adminPasswordLoginDisabled = false;

    // Countdown for blocked logins
    isBlocked = false;
    countdownSeconds = 0;
    private countdownInterval: any;

    constructor(
        private adminService: RdioScannerAdminService,
        private ngFormBuilder: FormBuilder,
        private route: ActivatedRoute,
        private router: Router,
    ) {
        this.form = this.ngFormBuilder.group({
            password: this.ngFormBuilder.control(null, Validators.required),
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

        // Check whether password login has been disabled server-side
        this.adminService.getLoginConfig().then(cfg => {
            this.adminPasswordLoginDisabled = cfg.adminPasswordLoginDisabled;
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
        this.form.disable();
        
        this.countdownInterval = setInterval(() => {
            this.countdownSeconds--;
            if (this.countdownSeconds <= 0) {
                clearInterval(this.countdownInterval);
                this.isBlocked = false;
                this.form.enable();
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

    /** SSO login: exchange the stored user PIN for an admin JWT. */
    async ssoLogin(): Promise<void> {
        const pin = window.localStorage.getItem('rdio-scanner-pin');
        const decodedPin = pin ? window.atob(pin) : null;
        if (!decodedPin) {
            this.ssoMessage = 'No active TLR session found. Please sign in to TLR first, then return here.';
            return;
        }
        this.ssoLoading = true;
        this.ssoMessage = '';
        const ok = await this.adminService.ssoLogin(decodedPin);
        this.ssoLoading = false;
        if (ok) {
            this.loggedIn.emit();
        } else {
            this.ssoMessage = 'SSO login failed. Make sure your account has System Admin privileges.';
        }
    }

    async login(password = this.form.get('password')?.value): Promise<void> {
        if (!password) {
            return;
        }

        this.form.disable();

        const loggedIn = await this.adminService.login(password);

        if (loggedIn) {
            this.loggedIn.emit();

        } else {
            this.form.enable();
            this.form.reset();

            this.message = 'Invalid password';
        }
    }
}
