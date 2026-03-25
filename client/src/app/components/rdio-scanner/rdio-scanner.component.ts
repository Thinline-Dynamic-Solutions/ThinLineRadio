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

import { Component, ElementRef, HostListener, OnDestroy, OnInit } from '@angular/core';
import { ActivatedRoute } from '@angular/router';
import { MatSnackBar } from '@angular/material/snack-bar';
import { timer } from 'rxjs';
import { RdioScannerEvent, RdioScannerLivefeedMode } from './rdio-scanner';
import { RdioScannerService } from './rdio-scanner.service';
import { SettingsService } from './settings/settings.service';
import { RdioScannerNativeComponent } from './native/native.component';

@Component({
    selector: 'rdio-scanner',
    styleUrls: ['./rdio-scanner.component.scss'],
    templateUrl: './rdio-scanner.component.html',
})
export class RdioScannerComponent implements OnDestroy, OnInit {
    private eventSubscription;

    private livefeedMode: RdioScannerLivefeedMode = RdioScannerLivefeedMode.Offline;
    
    userRegistrationEnabled = false;
    userAuthenticated = false;
    private pinAuthRequired = false;
    private connectionLimitAlertShown = false;

    constructor(
        private matSnackBar: MatSnackBar,
        private ngElementRef: ElementRef,
        private rdioScannerService: RdioScannerService,
        private settingsService: SettingsService,
        private route: ActivatedRoute,
    ) {
        this.eventSubscription = this.rdioScannerService.event.subscribe((event: RdioScannerEvent) => this.eventHandler(event));

        const initialConfig = (window as any)?.initialConfig;
        if (initialConfig?.options) {
            this.userRegistrationEnabled = !!initialConfig.options.userRegistrationEnabled;

            if (this.userRegistrationEnabled) {
                // Check if we're coming from a route that requires explicit authentication
                // (like /verify) - if so, don't auto-authenticate even if PIN exists
                const currentPath = this.route.snapshot.url.map(segment => segment.path).join('/');
                const requiresExplicitAuth = currentPath === 'verify' || 
                                            this.route.snapshot.queryParams['verify'] || 
                                            this.route.snapshot.queryParams['token'];
                
                if (!requiresExplicitAuth) {
                    const savedPin = this.rdioScannerService.readPin?.();
                    if (savedPin) {
                        this.userAuthenticated = true;
                        this.pinAuthRequired = false;
                    } else {
                        this.pinAuthRequired = true;
                    }
                } else {
                    // Coming from verify or similar route - require explicit authentication
                    this.userAuthenticated = false;
                    this.pinAuthRequired = true;
                }
            }
        }
    }

    onUserAuthenticated(): void {
        console.log('User authenticated, switching to main component');
        this.userAuthenticated = true;
        this.pinAuthRequired = false;
        if (!this.tryAuthenticateWithStoredPin()) {
            console.warn('No stored PIN available immediately after login; waiting for server challenge.');
        }
    }

    @HostListener('window:beforeunload', ['$event'])
    exitNotification(event: BeforeUnloadEvent): void {
        if (this.livefeedMode !== RdioScannerLivefeedMode.Offline) {
            event.preventDefault();

            event.returnValue = 'Live Feed is ON, do you really want to leave?';
        }
    }

    ngOnDestroy(): void {
        this.eventSubscription.unsubscribe();
    }

    ngOnInit(): void {
        /*
         * BEGIN OF RED TAPE:
         * 
         * By modifying, deleting or disabling the following lines, you harm
         * the open source project and its author.  Rdio Scanner represents a lot of
         * investment in time, support, testing and hardware.
         * 
         * Be respectful, sponsor the project, use native apps when possible.
         * 
         */
        timer(10000).subscribe(() => {
            const ua: string = navigator.userAgent;

            if (ua.includes('Android') || ua.includes('iPad') || ua.includes('iPhone')) {
                this.matSnackBar.openFromComponent(RdioScannerNativeComponent);
            }
        });
        /**
         * END OF RED TAPE.
         */
        
        // Load and apply saved font on app init
        this.loadAndApplyFont();
    }
    
    private loadAndApplyFont(): void {
        const availableFonts = [
            { name: 'Roboto', value: 'Roboto, sans-serif' },
            { name: 'Rajdhani', value: 'Rajdhani, sans-serif' },
            { name: 'ShareTechMono', value: '"Share Tech Mono", monospace' },
            { name: 'Audiowide', value: 'Audiowide, cursive' },
        ];
        
        this.settingsService.getSettings().subscribe({
            next: (settings) => {
                const fontName = settings?.appFont || 'Roboto';
                const font = availableFonts.find(f => f.name === fontName);
                if (font) {
                    document.body.style.fontFamily = font.value;
                    
                    // Adjust font size for Audiowide (15% smaller)
                    if (fontName === 'Audiowide') {
                        document.documentElement.style.fontSize = '14.45px'; // 85% of 17px
                    } else {
                        document.documentElement.style.fontSize = '';
                    }
                }
            },
            error: () => {
                // On error, use default font (Roboto)
                document.body.style.fontFamily = 'Roboto, sans-serif';
            },
        });
    }

    start(): void {
        this.rdioScannerService.startLivefeed();
    }

    stop(): void {
        this.rdioScannerService.stopLivefeed();
    }

    toggleFullscreen(): void {
        if (document.fullscreenElement) {
            const el: {
                exitFullscreen?: () => void;
                mozCancelFullScreen?: () => void;
                msExitFullscreen?: () => void;
                webkitExitFullscreen?: () => void;
            } = document;

            if (el.exitFullscreen) {
                el.exitFullscreen();

            } else if (el.mozCancelFullScreen) {
                el.mozCancelFullScreen();

            } else if (el.msExitFullscreen) {
                el.msExitFullscreen();

            } else if (el.webkitExitFullscreen) {
                el.webkitExitFullscreen();
            }

        } else {
            const el = this.ngElementRef.nativeElement;

            if (el.requestFullscreen) {
                el.requestFullscreen();

            } else if (el.mozRequestFullScreen) {
                el.mozRequestFullScreen();

            } else if (el.msRequestFullscreen) {
                el.msRequestFullscreen();

            } else if (el.webkitRequestFullscreen) {
                el.webkitRequestFullscreen();
            }
        }
    }

    private eventHandler(event: RdioScannerEvent): void {
        if (event.livefeedMode) {
            this.livefeedMode = event.livefeedMode;
        }
        
        if ('auth' in event) {
            if (event.auth) {
                // Check if too many connections
                if (event.tooMany) {
                    console.log('Connection rejected: too many concurrent connections');
                    
                    // Clear PIN immediately to prevent retries
                    this.rdioScannerService.clearPin();
                    
                    // Update state immediately
                    this.pinAuthRequired = true;
                    this.userAuthenticated = false;
                    
                    // Show browser alert with helpful message (only once)
                    if (!this.connectionLimitAlertShown) {
                        this.connectionLimitAlertShown = true;
                        
                        const limit = event.connectionLimit || 0;
                        const limitText = limit > 0 ? `Your connection limit is ${limit}.` : '';
                        const message = `You have reached your connection limit. ${limitText}\n\nPlease close any other active sessions, reload this page, and try logging in again.`;
                        alert(message);
                        
                        // Reset the flag after 10 seconds in case they want to try again
                        setTimeout(() => {
                            this.connectionLimitAlertShown = false;
                        }, 10000);
                    }
                    
                    return;
                }
                
                // Try to authenticate - the service will handle not sending PIN if expired
                const authenticated = this.tryAuthenticateWithStoredPin();
                if (authenticated) {
                    console.log('Submitted stored PIN in response to websocket challenge');
                }

                this.pinAuthRequired = !authenticated;
                this.userAuthenticated = authenticated;

            } else {
                this.pinAuthRequired = false;
                this.userAuthenticated = true;
            }
        }
        
        // Handle expired PIN - don't force re-authentication, let them stay logged in
        // but the main component will block access and show checkout
        if ('expired' in event && event.expired === true) {
            console.log('PIN expired, user will be locked out but can stay logged in');
            // Don't clear PIN or force re-auth - let them stay logged in
            // The main component will handle blocking access
        }
        
        if ('config' in event) {
            this.userRegistrationEnabled = event.config?.options?.userRegistrationEnabled ?? false;

            if (this.userRegistrationEnabled) {
                // Check if we're coming from a route that requires explicit authentication
                // (like /verify) - if so, don't auto-authenticate even if PIN exists
                const currentPath = this.route.snapshot.url.map(segment => segment.path).join('/');
                const requiresExplicitAuth = currentPath === 'verify' || 
                                            this.route.snapshot.queryParams['verify'] || 
                                            this.route.snapshot.queryParams['token'];
                
                if (!requiresExplicitAuth) {
                    const savedPin = this.rdioScannerService.readPin?.();
                    if (savedPin) {
                        this.userAuthenticated = true;
                        this.pinAuthRequired = false;
                    } else {
                        this.userAuthenticated = false;
                        this.pinAuthRequired = true;
                    }
                } else {
                    // Coming from verify or similar route - require explicit authentication
                    this.userAuthenticated = false;
                    this.pinAuthRequired = true;
                }
            } else {
                this.pinAuthRequired = false;
            }
        }
    }

    hasVerificationToken(): boolean {
        const token = this.route.snapshot.queryParams['verify'] || this.route.snapshot.queryParams['token'];
        return !!token;
    }

    get authRequired(): boolean {
        return this.userRegistrationEnabled || this.pinAuthRequired;
    }

    private tryAuthenticateWithStoredPin(): boolean {
        const savedPin = this.rdioScannerService.readPin?.();
        if (!savedPin) {
            return false;
        }
        this.rdioScannerService.authenticate(savedPin);

        return true;
    }

    onSignOut(): void {
        // Clear authentication state
        this.userAuthenticated = false;
        this.pinAuthRequired = true;
        
        // Disconnect WebSocket connection and clear PIN
        this.rdioScannerService.disconnect();
        
        // Stop livefeed if running
        this.stop();
    }

}
