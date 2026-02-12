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

import { DOCUMENT } from '@angular/common';
import { EventEmitter, Inject, Injectable, OnDestroy } from '@angular/core';
import { Router } from '@angular/router';
import { interval, Subscription, timer } from 'rxjs';
import { takeWhile } from 'rxjs/operators';
import { AppUpdateService } from '../../shared/update/update.service';
import {
    RdioScannerAvoidOptions,
    RdioScannerBeepStyle,
    RdioScannerCall,
    RdioScannerCategory,
    RdioScannerCategoryStatus,
    RdioScannerCategoryType,
    RdioScannerConfig,
    RdioScannerEvent,
    RdioScannerLivefeed,
    RdioScannerLivefeedMap,
    RdioScannerLivefeedMode,
    RdioScannerOscillatorData,
    RdioScannerPlaybackList,
    RdioScannerSearchOptions,
} from './rdio-scanner';

declare global {
    interface Window {
        webkitAudioContext: typeof AudioContext;
    }
}

enum WebsocketCallFlag {
    Download = 'd',
    Play = 'p',
}

enum WebsocketCommand {
    Alert = 'ALT',
    Call = 'CAL',
    Config = 'CFG',
    Error = 'ERR',
    Expired = 'XPR',
    ListCall = 'LCL',
    ListenersCount = 'LSC',
    LivefeedMap = 'LFM',
    Max = 'MAX',
    Pin = 'PIN',
    PinSet = 'PNS',
    Version = 'VER',
}

@Injectable()
export class RdioScannerService implements OnDestroy {
    static LOCAL_STORAGE_KEY_LEGACY = 'rdio-scanner';
    static LOCAL_STORAGE_KEY_LFM = 'rdio-scanner-lfm';
    static LOCAL_STORAGE_KEY_PIN = 'rdio-scanner-pin';
    static LOCAL_STORAGE_KEY_HOLD_SYS = 'rdio-scanner-hold-sys';
    static LOCAL_STORAGE_KEY_HOLD_TG = 'rdio-scanner-hold-tg';
    static LOCAL_STORAGE_KEY_HIDDEN_SYSTEMS = 'rdio-scanner-hidden-systems';

    event = new EventEmitter<RdioScannerEvent>();

    private pinExpired: boolean = false;

    private audioContext: AudioContext | undefined;

    private audioSource: AudioBufferSourceNode | undefined;
    private audioGainNode: GainNode | undefined;
    private audioSourceStartTime = NaN;
    private volume = 1.0; // Volume level (0.0 to 1.0)

    private call: RdioScannerCall | undefined;
    private callPrevious: RdioScannerCall | undefined;
    private callQueue: RdioScannerCall[] = [];

    private categories: RdioScannerCategory[] = [];

    private config: RdioScannerConfig = {
        dimmerDelay: false,
        groups: {},
        groupsData: [],
        keypadBeeps: undefined,
        playbackGoesLive: false,
        showListenersCount: false,
        systems: [],
        tags: {},
        tagsData: [],
        time12hFormat: false,
    };

    getConfig(): RdioScannerConfig {
        return this.config;
    }

    private instanceId = 'default';

    private livefeedMap = {} as RdioScannerLivefeedMap;
    private livefeedMapPriorToHoldSystem: RdioScannerLivefeedMap | undefined;
    private livefeedMapPriorToHoldTalkgroup: RdioScannerLivefeedMap | undefined;
    private livefeedMode = RdioScannerLivefeedMode.Offline;
    private livefeedModePriorToPlayback: RdioScannerLivefeedMode | undefined;
    private livefeedPaused = false;

    private oscillatorContext: AudioContext | undefined;

    private playbackList: RdioScannerPlaybackList | undefined;
    private playbackPending: number | undefined;
    private playbackRefreshing = false;
    private isOneOffPlayback = false; // Track if this is a one-off playback (e.g., from alerts) vs playback list

    private skipDelay: Subscription | undefined;

    private websocket: WebSocket | undefined;
    private linked = false;
    private isReconnecting = false;
    private reconnectAttempts = 0;
    private reconnectDelay = 2000; // Start with 2 seconds

    constructor(
        appUpdateService: AppUpdateService,
        private router: Router,
        @Inject(DOCUMENT) private document: Document,
    ) {
        if (router.url.endsWith('/reset')) {
            window?.localStorage?.clear();

            router.navigateByUrl(router.url.replace('/reset', ''), {
                replaceUrl: true,
            }).then(() => window?.location?.reload());

            return;
        }

        this.bootstrapAudio();

        this.initializeInstanceId();

        this.readLivefeedMap();

        this.loadHoldStates();

        this.openWebsocket();
    }

    authenticate(password: string): void {
        // Don't send PIN if it's expired (prevents authentication loop)
        if (this.pinExpired) {
            return;
        }
        this.sendtoWebsocket(WebsocketCommand.Pin, window.btoa(password));
    }

    avoid(options: RdioScannerAvoidOptions = {}): void {
        const clearTimer = (lfm: RdioScannerLivefeed): void => {
            lfm.minutes = undefined;
            lfm.timer?.unsubscribe();
            lfm.timer = undefined;
        };

        const setTimer = (lfm: RdioScannerLivefeed, minutes: number): void => {
            lfm.minutes = minutes;
            lfm.timer = timer(minutes * 60 * 1000).subscribe(() => {
                lfm.active = true;
                lfm.minutes = undefined;
                lfm.timer = undefined;

                this.rebuildCategories();
                this.saveLivefeedMap();

                this.event.emit({
                    categories: this.categories,
                    map: this.livefeedMap,
                });
            });
        };

        if (this.livefeedMapPriorToHoldSystem) {
            this.livefeedMapPriorToHoldSystem = undefined;
        }

        if (this.livefeedMapPriorToHoldTalkgroup) {
            this.livefeedMapPriorToHoldTalkgroup = undefined;
        }

        // Save cleared hold states to local storage
        this.saveHoldStates();

        if (typeof options.all === 'boolean') {
            Object.keys(this.livefeedMap).map((sys: string) => +sys).forEach((sys: number) => {
                Object.keys(this.livefeedMap[sys]).map((tg: string) => +tg).forEach((tg: number) => {
                    const lfm = this.livefeedMap[sys][tg];
                    clearTimer(lfm);
                    lfm.active = typeof options.status === 'boolean' ? options.status : !!options.all;
                });
            });

        } else if (options.call) {
            const lfm = this.livefeedMap[options.call.system][options.call.talkgroup];
            clearTimer(lfm);
            lfm.active = typeof options.status === 'boolean' ? options.status : !lfm.active;
            if (typeof options.minutes === 'number') setTimer(lfm, options.minutes);

        } else if (options.system && options.talkgroup) {
            const lfm = this.livefeedMap[options.system.id][options.talkgroup.id];
            clearTimer(lfm);
            lfm.active = typeof options.status === 'boolean' ? options.status : !lfm.active;
            if (typeof options.minutes === 'number') setTimer(lfm, options.minutes);

        } else if (options.system && !options.talkgroup) {
            const sys = options.system.id;
            Object.keys(this.livefeedMap[sys]).map((tg: string) => +tg).forEach((tg: number) => {
                const lfm = this.livefeedMap[sys][tg];
                clearTimer(lfm);
                lfm.active = typeof options.status === 'boolean' ? options.status : !lfm.active;
            });

        } else {
            const call = this.call || this.callPrevious;
            if (call) {
                const lfm = this.livefeedMap[call.system][call.talkgroup];
                clearTimer(lfm);
                lfm.active = typeof options.status === 'boolean' ? options.status : !lfm.active;
                if (typeof options.minutes === 'number') setTimer(lfm, options.minutes);
            }
        }

        if (this.livefeedMode !== RdioScannerLivefeedMode.Playback) {
            this.cleanQueue();
        }

        this.rebuildCategories();

        this.saveLivefeedMap();

        if (this.livefeedMode === RdioScannerLivefeedMode.Online) {
            this.startLivefeed();
        }

        this.event.emit({
            categories: this.categories,
            holdSys: false,
            holdTg: false,
            map: this.livefeedMap,
            queue: this.callQueue.length,
        });
    }

    async beep(style = RdioScannerBeepStyle.Activate): Promise<void> {
        const seq = this.config.keypadBeeps && this.config.keypadBeeps[style];

        if (seq) await this.playOscillatorSequence(seq);
    }

    clearPin(): void {
        window?.localStorage.removeItem(RdioScannerService.LOCAL_STORAGE_KEY_PIN);
    }

    disconnect(): void {
        // Reset reconnection flags to prevent automatic reconnection
        this.isReconnecting = false;
        this.reconnectAttempts = 0;
        // Close the WebSocket with code 1000 (clean close) to prevent reconnection
        this.closeWebsocket();
        // Also clear PIN and reset state
        this.clearPin();
        this.linked = false;
        this.pinExpired = false;
    }

    clearHoldStates(): void {
        window?.localStorage.removeItem(RdioScannerService.LOCAL_STORAGE_KEY_HOLD_SYS);
        window?.localStorage.removeItem(RdioScannerService.LOCAL_STORAGE_KEY_HOLD_TG);
        this.pendingHoldSys = false;
        this.pendingHoldTg = false;
    }

    private saveHoldStates(): void {
        if (this.livefeedMapPriorToHoldSystem) {
            window?.localStorage?.setItem(RdioScannerService.LOCAL_STORAGE_KEY_HOLD_SYS, 'true');
        } else {
            window?.localStorage?.removeItem(RdioScannerService.LOCAL_STORAGE_KEY_HOLD_SYS);
        }

        if (this.livefeedMapPriorToHoldTalkgroup) {
            window?.localStorage?.setItem(RdioScannerService.LOCAL_STORAGE_KEY_HOLD_TG, 'true');
        } else {
            window?.localStorage?.removeItem(RdioScannerService.LOCAL_STORAGE_KEY_HOLD_TG);
        }
    }

    private loadHoldStates(): void {
        const holdSys = window?.localStorage?.getItem(RdioScannerService.LOCAL_STORAGE_KEY_HOLD_SYS) === 'true';
        const holdTg = window?.localStorage?.getItem(RdioScannerService.LOCAL_STORAGE_KEY_HOLD_TG) === 'true';

        // If we have hold states saved, we need to restore them when the livefeed map is available
        if (holdSys || holdTg) {
            // Store the flags to restore later when we have a call and livefeed map
            this.pendingHoldSys = holdSys;
            this.pendingHoldTg = holdTg;
        }
    }

    private pendingHoldSys = false;
    private pendingHoldTg = false;

    holdSystem(options?: { resubscribe?: boolean }): void {
        const call = this.call || this.callPrevious;

        if (call && this.livefeedMap) {
            if (this.livefeedMapPriorToHoldSystem) {
                this.livefeedMap = this.livefeedMapPriorToHoldSystem;

                this.livefeedMapPriorToHoldSystem = undefined;

                // Save released hold state to local storage
                this.saveHoldStates();
            } else {
                if (this.livefeedMapPriorToHoldTalkgroup) {
                    this.holdTalkgroup({ resubscribe: false });
                }

                this.livefeedMapPriorToHoldSystem = this.livefeedMap;

                this.livefeedMap = Object.keys(this.livefeedMap).map((sys) => +sys).reduce((sysMap, sys) => {
                    const allOn = Object.keys(this.livefeedMap[sys]).map((tg) => +tg).every((tg) => !this.livefeedMap[sys][tg]);

                    sysMap[sys] = Object.keys(this.livefeedMap[sys]).map((tg) => +tg).reduce((tgMap, tg) => {
                        this.livefeedMap[sys][tg].timer?.unsubscribe();

                        tgMap[tg] = {
                            active: sys === call.system ? allOn || this.livefeedMap[sys][tg].active : false,
                        } as RdioScannerLivefeed;

                        return tgMap;
                    }, {} as { [key: number]: RdioScannerLivefeed });

                    return sysMap;
                }, {} as RdioScannerLivefeedMap);

                this.cleanQueue();
            }

            this.rebuildCategories();

            if (typeof options?.resubscribe !== 'boolean' || options.resubscribe) {
                if (this.livefeedMode === RdioScannerLivefeedMode.Online) {
                    this.startLivefeed();
                }
            }

            this.event.emit({
                categories: this.categories,
                holdSys: !!this.livefeedMapPriorToHoldSystem,
                holdTg: false,
                map: this.livefeedMap,
                queue: this.callQueue.length,
            });

            // Save hold states to local storage
            this.saveHoldStates();
        }
    }

    holdTalkgroup(options?: { resubscribe?: boolean }): void {
        const call = this.call || this.callPrevious;

        if (call && this.livefeedMap) {
            if (this.livefeedMapPriorToHoldTalkgroup) {
                this.livefeedMap = this.livefeedMapPriorToHoldTalkgroup;

                this.livefeedMapPriorToHoldTalkgroup = undefined;

                // Save released hold state to local storage
                this.saveHoldStates();
            } else {
                if (this.livefeedMapPriorToHoldSystem) {
                    this.holdSystem({ resubscribe: false });
                }

                this.livefeedMapPriorToHoldTalkgroup = this.livefeedMap;

                this.livefeedMap = Object.keys(this.livefeedMap).map((sys) => +sys).reduce((sysMap, sys) => {
                    sysMap[sys] = Object.keys(this.livefeedMap[sys]).map((tg) => +tg).reduce((tgMap, tg) => {
                        this.livefeedMap[sys][tg].timer?.unsubscribe();

                        tgMap[tg] = {
                            active: sys === call.system ? tg === call.talkgroup : false,
                        } as RdioScannerLivefeed;

                        return tgMap;
                    }, {} as { [key: number]: RdioScannerLivefeed });

                    return sysMap;
                }, {} as RdioScannerLivefeedMap);

                this.cleanQueue();
            }

            this.rebuildCategories();

            if (typeof options?.resubscribe !== 'boolean' || options.resubscribe) {
                if (this.livefeedMode === RdioScannerLivefeedMode.Online) {
                    this.startLivefeed();
                }
            }

            this.event.emit({
                categories: this.categories,
                holdSys: false,
                holdTg: !!this.livefeedMapPriorToHoldTalkgroup,
                map: this.livefeedMap,
                queue: this.callQueue.length,
            });

            // Save hold states to local storage
            this.saveHoldStates();
        }
    }

    isAvoided(call: RdioScannerCall): boolean {
        return !!this.livefeedMap[call.system] && this.livefeedMap[call.system][call.talkgroup]?.active !== true;
    }

    isAvoidedTimer(call: RdioScannerCall): number {
        if (!!this.livefeedMap[call.system] && this.livefeedMap[call.system][call.talkgroup]?.minutes !== undefined) {
            return this.livefeedMap[call.system][call.talkgroup]?.minutes || 0;
        }
        return 0;
    }

    isPatched(call: RdioScannerCall): boolean {
        return this.isAvoided(call) && call.patches?.some((tg) => {
            return !!this.livefeedMap[call.system] && this.livefeedMap[call.system][tg]?.active || false;
        });
    }

    livefeed(): void {
        if (this.livefeedMode === RdioScannerLivefeedMode.Offline) {
            this.startLivefeed();

        } else if (this.livefeedMode === RdioScannerLivefeedMode.Online) {
            this.stopLivefeed();

        } else if (this.livefeedMode === RdioScannerLivefeedMode.Playback) {
            this.stopPlaybackMode();
        }
    }

    loadAndDownload(id: number): void {
        if (!id) {
            return;
        }

        this.getCall(id, WebsocketCallFlag.Download);
    }

    loadAndPlay(id: number): void {
        if (!id) {
            return;
        }

        if (this.skipDelay) {
            this.skipDelay.unsubscribe();

            this.skipDelay = undefined;
        }

        this.playbackPending = id;

        this.stop();

        // Mark as one-off playback if we don't have a playbackList
        // This prevents auto-advancing through old search results when playing from alerts
        this.isOneOffPlayback = !this.playbackList;
        
        // Clear the call queue when playing a one-off call to prevent queued calls from playing
        // This is especially important when coming from the playback screen where calls may be queued
        if (this.isOneOffPlayback) {
            this.clearQueue();
            // Emit queue update to reflect cleared queue in UI
            this.event.emit({ queue: 0 });
        }
        
        if (this.livefeedMode === RdioScannerLivefeedMode.Offline) {
            // Store previous mode before entering playback (should be Offline in this case)
            this.livefeedModePriorToPlayback = this.livefeedMode;
            this.livefeedMode = RdioScannerLivefeedMode.Playback;
            // Clear any existing playbackList when switching to playback mode for a one-off call
            if (this.isOneOffPlayback) {
                this.playbackList = undefined;
            }

            if (this.livefeedMapPriorToHoldSystem) {
                this.holdSystem({ resubscribe: false });
            }

            if (this.livefeedMapPriorToHoldTalkgroup) {
                this.holdTalkgroup({ resubscribe: false });
            }

            this.event.emit({ livefeedMode: this.livefeedMode, playbackPending: id, queue: this.isOneOffPlayback ? 0 : undefined });
        } else if (this.livefeedMode === RdioScannerLivefeedMode.Online) {
            // Keep live feed on - don't switch to Playback mode if live feed is already running
            // Just play the call while live feed continues in the background
            // Clear any existing playbackList when switching to playback mode for a one-off call
            if (this.isOneOffPlayback) {
                this.playbackList = undefined;
            }

            this.event.emit({ livefeedMode: this.livefeedMode, playbackPending: id, queue: this.isOneOffPlayback ? 0 : undefined });

        } else if (this.livefeedMode === RdioScannerLivefeedMode.Playback) {
            // If we're already in playback mode but don't have a playbackList, 
            // this is likely a one-off call - clear any stale playbackList
            if (this.isOneOffPlayback) {
                this.playbackList = undefined;
            }
            this.event.emit({ playbackPending: id, queue: this.isOneOffPlayback ? 0 : undefined });
        }

        this.getCall(id, WebsocketCallFlag.Play);
    }

    ngOnDestroy(): void {
        this.isReconnecting = false; // Stop reconnection attempts
        this.closeWebsocket();

        this.stop();
    }

    pause(status = !this.livefeedPaused): void {
        this.livefeedPaused = status;

        if (status) {
            this.audioContext?.suspend();

        } else {
            this.audioContext?.resume();

            this.play();
        }

        this.event.emit({ pause: this.livefeedPaused });
    }

    play(call?: RdioScannerCall | undefined): void {
        if (this.livefeedPaused || this.skipDelay) {
            return;

        } else if (call?.audio) {
            if (this.call) {
                this.stop({ emit: false });
            }

            this.call = call;

        } else if (this.call) {
            return;

        } else {
            this.call = this.callQueue.shift();
        }

        if (!this.call?.audio) {
            return;
        }

        const queue = this.livefeedMode === RdioScannerLivefeedMode.Playback
            ? this.getPlaybackQueueCount()
            : this.callQueue.length;

        const arrayBuffer = new ArrayBuffer(this.call.audio.data.length);
        const arrayBufferView = new Uint8Array(arrayBuffer);

        for (let i = 0; i < (this.call.audio.data.length); i++) {
            arrayBufferView[i] = this.call.audio.data[i];
        }

        this.audioContext?.decodeAudioData(arrayBuffer, async (buffer) => {
            if (!this.audioContext || this.audioSource || !this.call) {
                return;
            }

            await this.playAlert(this.call);

            this.audioSource = this.audioContext.createBufferSource();
            this.audioSource.buffer = buffer;
            
            // Create gain node for volume control if it doesn't exist
            if (!this.audioGainNode) {
                this.audioGainNode = this.audioContext.createGain();
                this.audioGainNode.gain.value = this.volume;
                this.audioGainNode.connect(this.audioContext.destination);
            }
            
            this.audioSource.connect(this.audioGainNode);
            this.audioSource.onended = () => this.skip({ delay: true });
            this.audioSource.start();

            this.event.emit({ call: this.call, queue });

            interval(500).pipe(takeWhile(() => !!this.call)).subscribe(() => {
                if (this.audioContext && !isNaN(this.audioContext.currentTime)) {
                    if (isNaN(this.audioSourceStartTime)) {
                        this.audioSourceStartTime = this.audioContext.currentTime;
                    }

                    if (!this.livefeedPaused) {
                        this.event.emit({ time: this.audioContext.currentTime - this.audioSourceStartTime });
                    }
                }
            });
        }, () => {
            this.event.emit({ call: this.call, queue });

            this.skip({ delay: false });
        });
    }

    queue(call: RdioScannerCall, options?: { priority?: boolean }): void {
        if (!call?.audio || this.livefeedMode === RdioScannerLivefeedMode.Offline) {
            return;
        }

        if (options?.priority) {
            this.callQueue.unshift(call);

        } else {
            this.callQueue.push(call);
        }

        if (this.audioSource || this.call || this.livefeedPaused || this.skipDelay) {
            this.event.emit({
                queue: this.livefeedMode === RdioScannerLivefeedMode.Online ? this.callQueue.length : this.getPlaybackQueueCount(),
            });

        } else {
            this.play();
        }
    }

    replay(): void {
        this.play(this.call || this.callPrevious);
    }

    readPin(): string | undefined {
        const pin = window?.localStorage?.getItem(RdioScannerService.LOCAL_STORAGE_KEY_PIN);

        return pin ? window.atob(pin) : undefined;
    }

    savePin(pin: string): void {
        window?.localStorage?.setItem(RdioScannerService.LOCAL_STORAGE_KEY_PIN, window.btoa(pin));
    }

    searchCalls(options: RdioScannerSearchOptions): void {
        this.sendtoWebsocket(WebsocketCommand.ListCall, options);
    }

    skip(options?: { delay?: boolean }): void {
        const play = () => {
            if (this.livefeedMode === RdioScannerLivefeedMode.Playback) {
                // Only auto-advance if we have a playback list AND this is not a one-off playback
                // If no playback list or it's a one-off playback (e.g., from alerts) - stop playback mode
                if (this.playbackList && !this.isOneOffPlayback) {
                    this.playbackNextCall();
                } else {
                    // One-off playback finished - exit playback mode and reset flag
                    this.isOneOffPlayback = false;
                    this.stopPlaybackMode();
                }

            } else {
                this.play();
            }
        };

        this.stop();

        if (options?.delay) {
            this.skipDelay = timer(1000).subscribe(() => {
                this.skipDelay = undefined;

                play();
            });

        } else {
            if (this.skipDelay) {
                this.skipDelay?.unsubscribe();

                this.skipDelay = undefined;
            }

            play();
        }
    }

    startLivefeed(): void {
        // Get hidden systems from localStorage
        let hiddenSystems: Set<number> = new Set();
        try {
            const stored = window?.localStorage?.getItem(RdioScannerService.LOCAL_STORAGE_KEY_HIDDEN_SYSTEMS);
            if (stored) {
                const hiddenIds: number[] = JSON.parse(stored);
                hiddenSystems = new Set(hiddenIds);
            }
        } catch (error) {
            // Ignore errors reading hidden systems
        }

        // Build livefeed map, excluding hidden systems
        const lfm = Object.keys(this.livefeedMap).reduce((sysMap: { [key: number]: { [key: number]: boolean } }, sys) => {
            const systemId = +sys;
            // Skip hidden systems
            if (hiddenSystems.has(systemId)) {
                return sysMap;
            }
            
            sysMap[systemId] = Object.keys(this.livefeedMap[systemId]).reduce((tgMap: { [key: number]: boolean }, tg: string) => {
                tgMap[+tg] = this.livefeedMap[systemId][+tg].active;
                return tgMap;
            }, {});
            return sysMap;
        }, {});

        this.livefeedMode = RdioScannerLivefeedMode.Online;

        this.event.emit({ livefeedMode: this.livefeedMode });

        this.sendtoWebsocket(WebsocketCommand.LivefeedMap, lfm);
    }

    stop(options?: { emit?: boolean }): void {
        if (this.audioSource) {
            this.audioSource.onended = null;
            this.audioSource.stop();
            this.audioSource.disconnect();
            this.audioSource = undefined;
            this.audioSourceStartTime = NaN;
        }

        if (this.call) {
            this.callPrevious = this.call;

            this.call = undefined;
        }

        if (typeof options?.emit !== 'boolean' || options.emit) {
            this.event.emit({ call: this.call });
        }
    }

    stopLivefeed(): void {
        this.livefeedMode = RdioScannerLivefeedMode.Offline;

        this.clearQueue();

        this.event.emit({ livefeedMode: this.livefeedMode, queue: 0 });

        this.stop();

        this.sendtoWebsocket(WebsocketCommand.LivefeedMap, null);
    }

    stopPlaybackMode(): void {
        // Only stop playback mode if we're actually in playback mode
        // This prevents disabling live feed when search panel closes if we were never in playback mode
        if (this.livefeedMode !== RdioScannerLivefeedMode.Playback) {
            // Clear playback list even if not in playback mode (in case it exists)
            this.playbackList = undefined;
            return;
        }

        // Restore previous livefeed mode if it was saved (e.g., was Online before entering playback)
        if (this.livefeedModePriorToPlayback !== undefined) {
            this.livefeedMode = this.livefeedModePriorToPlayback;
            this.livefeedModePriorToPlayback = undefined;
            
            // If restoring to Online mode, restart livefeed
            if (this.livefeedMode === RdioScannerLivefeedMode.Online) {
                this.startLivefeed();
            }
        } else {
            // No previous mode saved, set to Offline (default)
            this.livefeedMode = RdioScannerLivefeedMode.Offline;
        }

        this.playbackRefreshing = false;
        this.isOneOffPlayback = false; // Reset one-off flag when stopping playback mode

        // Clear playback list when stopping playback mode
        // This ensures old search results don't persist when the playback screen is closed
        this.playbackList = undefined;

        this.clearQueue();

        this.event.emit({ livefeedMode: this.livefeedMode, queue: 0, playbackList: undefined });

        this.stop();
    }

    toggleCategory(category: RdioScannerCategory): void {
        const clearTimer = (lfm: RdioScannerLivefeed): void => {
            lfm.minutes = 0;
            lfm.timer?.unsubscribe();
            lfm.timer = undefined;
        };

        if (category) {
            if (this.livefeedMapPriorToHoldSystem) {
                this.livefeedMapPriorToHoldSystem = undefined;
            }

            if (this.livefeedMapPriorToHoldTalkgroup) {
                this.livefeedMapPriorToHoldTalkgroup = undefined;
            }

            // Save cleared hold states to local storage
            this.saveHoldStates();

            const status = category.status === RdioScannerCategoryStatus.On ? false : true;

            this.config?.systems.forEach((sys) => {
                sys.talkgroups?.forEach((tg) => {
                    const lfm = this.livefeedMap[sys.id][tg.id];

                    if (category.type == RdioScannerCategoryType.Group && tg.groups.includes(category.label)) {
                        clearTimer(lfm);
                        lfm.active = status;

                    } else if (category.type == RdioScannerCategoryType.Tag && tg.tag === category.label) {
                        clearTimer(lfm);
                        lfm.active = status;
                    }
                });
            });

            this.rebuildCategories();

            if (this.call && !this.livefeedMap[this.call.system] && this.livefeedMap[this.call.system][this.call.talkgroup]) {
                clearTimer(this.livefeedMap[this.call.system][this.call.talkgroup]);
                this.skip();
            }

            if (this.livefeedMode === RdioScannerLivefeedMode.Online) {
                this.startLivefeed();
            }

            this.saveLivefeedMap();

            this.cleanQueue();

            this.event.emit({
                categories: this.categories,
                holdSys: false,
                holdTg: false,
                map: this.livefeedMap,
                queue: this.callQueue.length,
            });
        }
    }

    private bootstrapAudio(): void {
        const events = ['keydown', 'mousedown', 'touchstart'];

        const bootstrap = async () => {
            if (!this.audioContext) {
                this.audioContext = new (window.AudioContext || window.webkitAudioContext)({ latencyHint: 'playback' });
            }

            if (!this.oscillatorContext) {
                this.oscillatorContext = new (window.AudioContext || window.webkitAudioContext)({ latencyHint: 'interactive' });
            }

            if (this.audioContext) {
                const resume = () => {
                    if (!this.livefeedPaused) {
                        if (this.audioContext?.state === 'suspended') {
                            this.audioContext?.resume().then(() => resume());
                        }
                    }
                };

                await this.audioContext.resume();

                this.audioContext.onstatechange = () => resume();
            }

            if (this.oscillatorContext) {
                const resume = () => {
                    if (this.oscillatorContext?.state === 'suspended') {
                        this.oscillatorContext?.resume().then(() => resume());
                    }
                };

                await this.oscillatorContext.resume();

                this.oscillatorContext.onstatechange = () => resume();
            }

            if (this.audioContext && this.oscillatorContext) {
                events.forEach((event) => document.body.removeEventListener(event, bootstrap));
                
                // Create gain node if audio context exists but gain node doesn't
                if (this.audioContext && !this.audioGainNode) {
                    this.audioGainNode = this.audioContext.createGain();
                    this.audioGainNode.gain.value = this.volume;
                    this.audioGainNode.connect(this.audioContext.destination);
                }
            }
        };

        events.forEach((event) => document.body.addEventListener(event, bootstrap));
    }

    // Set volume (0.0 to 1.0)
    setVolume(volume: number): void {
        this.volume = Math.max(0, Math.min(1, volume)); // Clamp between 0 and 1
        if (this.audioGainNode) {
            this.audioGainNode.gain.value = this.volume;
        }
    }

    // Get current volume (0.0 to 1.0)
    getVolume(): number {
        return this.volume;
    }

    // Public method to force initialize audio (for PWA auto-start)
    async ensureAudioReady(): Promise<boolean> {
        try {
            // Create audio contexts if they don't exist
            if (!this.audioContext) {
                this.audioContext = new (window.AudioContext || window.webkitAudioContext)({ latencyHint: 'playback' });
            }

            if (!this.oscillatorContext) {
                this.oscillatorContext = new (window.AudioContext || window.webkitAudioContext)({ latencyHint: 'interactive' });
            }

            // Aggressively try to resume both contexts
            // In PWA standalone mode, this should work without user interaction
            const resumePromises: Promise<void>[] = [];

            if (this.audioContext) {
                resumePromises.push(this.audioContext.resume().catch(() => {
                    console.log('Audio context resume attempted');
                }));
            }

            if (this.oscillatorContext) {
                resumePromises.push(this.oscillatorContext.resume().catch(() => {
                    console.log('Oscillator context resume attempted');
                }));
            }

            await Promise.all(resumePromises);

            // Set up aggressive state change handlers that keep trying to resume
            if (this.audioContext) {
                const resume = () => {
                    if (!this.livefeedPaused && this.audioContext?.state === 'suspended') {
                        this.audioContext?.resume().then(() => resume()).catch(() => {});
                    }
                };
                this.audioContext.onstatechange = () => resume();
            }

            if (this.oscillatorContext) {
                const resume = () => {
                    if (this.oscillatorContext?.state === 'suspended') {
                        this.oscillatorContext?.resume().then(() => resume()).catch(() => {});
                    }
                };
                this.oscillatorContext.onstatechange = () => resume();
            }

            // Return true even if not running - in PWA mode it will often start as 'suspended' 
            // but work once audio actually needs to play
            console.log(`Audio state - audioContext: ${this.audioContext?.state}, oscillatorContext: ${this.oscillatorContext?.state}`);
            return true;
        } catch (error) {
            console.error('Failed to initialize audio:', error);
            return false;
        }
    }

    isAudioReady(): boolean {
        return !!(this.audioContext && this.audioContext.state === 'running' && 
                  this.oscillatorContext && this.oscillatorContext.state === 'running');
    }

    private cleanQueue(): void {
        const isActive = (call: RdioScannerCall) => {
            const lfm = (sys: number, tg: number): boolean => this.livefeedMap && this.livefeedMap[sys] && this.livefeedMap[sys][tg]?.active;
            let active = lfm(call.system, call.talkgroup);
            if (!active && Array.isArray(call.patches)) {
                for (let i = 0; i < call.patches.length; i++) {
                    active = lfm(call.system, call.patches[i]);
                    if (active) {
                        break;
                    }
                }
            }
            return active;
        };

        this.callQueue = this.callQueue.filter((call: RdioScannerCall) => isActive(call));

        if (this.call && !isActive(this.call)) {
            this.skip();
        }
    }

    private clearQueue(): void {
        this.callQueue.splice(0, this.callQueue.length);
    }

    private closeWebsocket(): void {
        if (this.websocket instanceof WebSocket) {
            // Remove all event handlers first
            this.websocket.onclose = null;
            this.websocket.onerror = null;
            this.websocket.onmessage = null;
            this.websocket.onopen = null;

            // Only close if not already closed
            if (this.websocket.readyState !== WebSocket.CLOSED && this.websocket.readyState !== WebSocket.CLOSING) {
                try {
                    this.websocket.close(1000, 'Client closing connection');
                } catch (error) {
                    // Error closing WebSocket
                }
            }

            this.websocket = undefined;
            this.linked = false;
        }
    }

    private download(call: RdioScannerCall): void {
        if (call.audio) {
            const file = call.audio.data.reduce((str, val) => str += String.fromCharCode(val), '');
            const fileName = call.audioName || 'unknown.dat';
            const fileType = call.audioType || 'audio/*';
            const fileUri = `data:${fileType};base64,${window.btoa(file)}`;

            const el = this.document.createElement('a');

            el.style.display = 'none';

            el.setAttribute('href', fileUri);
            el.setAttribute('download', fileName);

            this.document.body.appendChild(el);

            el.click();

            this.document.body.removeChild(el);
        }
    }

    private getCall(id: number, flags?: WebsocketCallFlag): void {
        this.sendtoWebsocket(WebsocketCommand.Call, `${id}`, flags);
    }

    private getPlaybackQueueCount(id = this.call?.id || this.callPrevious?.id): number {
        let queueCount = 0;

        if (id && this.playbackList) {
            const index = this.playbackList.results.findIndex((call) => call.id === id);

            if (index !== -1) {
                if (this.playbackList.options.sort === -1) {
                    queueCount = this.playbackList.options.offset + index;

                } else {
                    queueCount = this.playbackList.count - this.playbackList.options.offset - index - 1;
                }
            }
        }

        return queueCount;
    }

    private initializeInstanceId(): void {
        this.instanceId = this.router.parseUrl(this.router.url).queryParams['id'] || this.instanceId;
    }

    private openWebsocket(): void {
        // Prevent multiple connections
        if (this.websocket && (this.websocket.readyState === WebSocket.CONNECTING || this.websocket.readyState === WebSocket.OPEN)) {
            return;
        }

        // Close any existing connection before creating a new one
        if (this.websocket) {
            this.closeWebsocket();
        }

        // Always use the root endpoint for WebSocket, regardless of current page URL
        // This ensures the WebSocket connects to the correct endpoint even if user is on /verify or other pages
        const websocketUrl = window.location.origin.replace(/^http/, 'ws');

        try {
            this.websocket = new WebSocket(websocketUrl);

            this.websocket.onerror = (error: Event) => {
                // Error will be followed by onclose, so we don't need to handle reconnection here
            };

            this.websocket.onclose = (ev: CloseEvent) => {
                this.linked = false;
                this.event.emit({ linked: this.linked });

                // Only reconnect if it wasn't a clean close (code 1000)
                // Note: We allow reconnection even if isReconnecting is true, because the previous reconnect attempt failed
                if (ev.code !== 1000) {
                    // Reset isReconnecting flag so we can schedule a new reconnect attempt
                    this.isReconnecting = false;
                    this.reconnectAttempts++;
                    // Retry every 2 seconds continuously
                    timer(this.reconnectDelay).subscribe(() => this.reconnectWebsocket());
                } else if (ev.code === 1000) {
                    this.reconnectAttempts = 0; // Reset on clean close
                    this.isReconnecting = false;
                }
            };

            this.websocket.onopen = () => {
                this.linked = true;
                this.isReconnecting = false;
                this.reconnectAttempts = 0; // Reset on successful connection
                this.event.emit({ linked: this.linked });

            if (this.websocket instanceof WebSocket) {
                this.websocket.onmessage = (ev: MessageEvent) => {
                    try {
                        this.parseWebsocketMessage(ev.data);
                    } catch (error) {
                        // Don't close connection on parse error
                    }
                };
            }

                this.sendtoWebsocket(WebsocketCommand.Version);
                this.sendtoWebsocket(WebsocketCommand.Config);
            };
        } catch (error) {
            this.linked = false;
            this.isReconnecting = false;
            this.event.emit({ linked: this.linked });
            // Schedule reconnect on error (keep trying every 2 seconds)
            this.reconnectAttempts++;
            timer(this.reconnectDelay).subscribe(() => this.reconnectWebsocket());
        }
    }

    private parseWebsocketMessage(message: string): void {
        let parsedMessage: any;
        try {
            parsedMessage = JSON.parse(message);
        } catch (error) {
            return;
        }

        if (Array.isArray(parsedMessage)) {
            const message = parsedMessage;
            switch (message[0]) {
                case WebsocketCommand.Alert:
                    if (message[1] !== null) {
                        const alertData = message[1];
                        this.event.emit({ alert: alertData });
                    }
                    break;
                case WebsocketCommand.Call:
                    if (message[1] !== null) {
                        const call: RdioScannerCall = message[1];
                        const flag: string = message[2];

                        if (flag === WebsocketCallFlag.Download) {
                            this.download(message[1]);

                        } else if (flag === WebsocketCallFlag.Play && call.id === this.playbackPending) {
                            this.playbackPending = undefined;

                            this.queue(this.transformCall(call), { priority: true });

                        } else {
                            this.queue(this.transformCall(call));
                        }

                        // Restore hold states if we have pending holds and this is the first call
                        if ((this.pendingHoldSys || this.pendingHoldTg) && this.livefeedMap && !this.call) {
                            this.restoreHoldStates(call);
                        }
                    }

                    break;

                case WebsocketCommand.Error:
                    // Handle server errors (e.g., delayed calls, access denied, etc.)
                    const errorMessage = message[1] || 'An error occurred';
                    console.error('Server error:', errorMessage);
                    
                    // Emit error event for UI to display
                    this.linked = true;
                    this.event.emit({
                        error: errorMessage,
                        linked: this.linked
                    });
                    
                    break;

                case WebsocketCommand.Config: {
                    const config = message[1];
                    
                    if (!config) {
                        return;
                    }

                    try {
                        this.config = {
                        alerts: config.alerts,
                        branding: typeof config.branding === 'string' ? config.branding : '',
                        dimmerDelay: typeof config.dimmerDelay === 'number' ? config.dimmerDelay : 5000,
                        email: typeof config.email === 'string' ? config.email : '',
                        groups: typeof config.groups !== null && typeof config.groups === 'object' ? config.groups : {},
                        groupsData: Array.isArray(config.groupsData) ? config.groupsData : [],
                        keypadBeeps: config.keypadBeeps !== null && typeof config.keypadBeeps === 'object' ? config.keypadBeeps : {},
                        options: typeof config.options === 'object' && config.options !== null ? config.options : undefined,
                        playbackGoesLive: typeof config.playbackGoesLive === 'boolean' ? config.playbackGoesLive : false,
                        showListenersCount: typeof config.showListenersCount === 'boolean' ? config.showListenersCount : false,
                        systems: Array.isArray(config.systems) ? config.systems.slice() : [],
                        tags: typeof config.tags !== null && typeof config.tags === 'object' ? config.tags : {},
                        tagsData: Array.isArray(config.tagsData) ? config.tagsData : [],
                        time12hFormat: typeof config.time12hFormat === 'boolean' ? config.time12hFormat : false,
                        userSettings: typeof config.userSettings === 'object' && config.userSettings !== null ? config.userSettings : undefined,
                    };

                        this.rebuildLivefeedMap();

                        if (this.livefeedMode === RdioScannerLivefeedMode.Online) {
                            this.startLivefeed();
                        }

                        this.linked = true;
                        this.pinExpired = false; // Reset expired flag on successful config
                        this.event.emit({
                            auth: false,
                            categories: this.categories,
                            config: this.config,
                            holdSys: !!this.livefeedMapPriorToHoldSystem,
                            holdTg: !!this.livefeedMapPriorToHoldTalkgroup,
                            linked: this.linked,
                            map: this.livefeedMap,
                        });
                    } catch (error) {
                        // Don't close connection on config error
                    }

                    break;
                }

                case WebsocketCommand.Expired:
                    this.pinExpired = true;
                    this.event.emit({ auth: true, expired: true });

                    break;

                case WebsocketCommand.ListCall:
                    this.playbackList = message[1];

                    if (this.playbackList) {
                        this.playbackList.results = this.playbackList.results.map((call) => this.transformCall(call));

                        this.event.emit({ playbackList: this.playbackList });

                        // Only auto-advance if we're in playback mode AND this is NOT a one-off playback
                        // This prevents auto-advancing when a ListCall arrives during a one-off playback from alerts
                        if (this.livefeedMode === RdioScannerLivefeedMode.Playback && !this.isOneOffPlayback) {
                            this.playbackNextCall();
                        }
                    }

                    break;

                case WebsocketCommand.ListenersCount:
                    this.event.emit({ listeners: message[1] });

                    break;

            case WebsocketCommand.PinSet: {
                const payload = message[1];
                if (typeof payload === 'string' && payload.length > 0) {
                    this.savePin(payload);
                    this.pinExpired = false; // Reset expired flag when new PIN is set
                }
                break;
            }

                case WebsocketCommand.Max:
                    // message[1] contains the connection limit
                    const connectionLimit = message[1] || 0;
                    this.event.emit({ auth: true, tooMany: true, connectionLimit: connectionLimit });

                    break;

                case WebsocketCommand.Pin:
                    // Server is requesting PIN authentication - try to authenticate automatically with stored PIN
                    // BUT: Don't send PIN if it's expired (to prevent loop)
                    if (this.pinExpired) {
                        this.event.emit({ auth: true, expired: true });
                        break;
                    }
                    
                    const storedPin = this.readPin();
                    if (storedPin) {
                        this.authenticate(storedPin);
                        // Don't emit auth event here - wait for server response (either success or MAX)
                    } else {
                        this.event.emit({ auth: true });
                    }

                    break;

                case WebsocketCommand.Version: {
                    const data = message[1];

                    if (data !== null && typeof data === 'object') {
                        const branding = data['branding'];
                        const email = data['email'];

                        if (typeof branding === 'string') {
                            this.config.branding = branding;
                        }

                        if (typeof email === 'string') {
                            this.config.email = email;
                        }

                        if (this.config.branding || this.config.email) {
                            this.event.emit({ config: this.config });
                        }
                    }

                    break;
                }
            }
        }
    }

    isLinked(): boolean {
        return this.linked;
    }

    private async playAlert(call: RdioScannerCall): Promise<void> {
        if (this.config.alerts) {
            let alert: string | undefined;

            call?.talkgroupData?.groups.some((label) => {
                const group = this.config.groupsData?.find((group) => group.label == label);

                if (group && group.alert) {
                    alert = group.alert;

                    return true;
                }

                return false;
            });

            if (!alert) {
                const tag = this.config.tagsData?.find((tag) => tag.label == call.talkgroupData?.tag);

                if (tag && tag.alert) alert = tag.alert;
            }

            if (!alert) alert = call.systemData?.alert;

            if (!alert) alert = call.talkgroupData?.alert;

            if (alert) await this.playOscillatorSequence(this.config.alerts[alert]);
            console.log(alert);
        }
    }

    private playbackNextCall(): void {
        if (this.call || this.livefeedMode !== RdioScannerLivefeedMode.Playback || !this.playbackList || this.playbackPending) {
            return;
        }

        const index = this.playbackList.results.findIndex((call) => call.id === this.callPrevious?.id);

        if (this.playbackList.options.sort === -1) {
            if (index === -1) {
                this.loadAndPlay(this.playbackList.results[this.playbackList.results.length - 1].id);

            } else if (index === 0) {
                if (this.playbackList.options.offset < this.playbackList.options.limit) {
                    if (this.playbackRefreshing) {
                        this.stopPlaybackMode();

                        if (this.config.playbackGoesLive) {
                            this.startLivefeed();
                        }

                    } else {
                        this.playbackRefreshing = true;
                        this.searchCalls(this.playbackList.options);
                    }

                } else {
                    this.searchCalls(Object.assign({}, this.playbackList.options, {
                        offset: this.playbackList.options.offset - this.playbackList.options.limit,
                    }));
                }

            } else {
                this.loadAndPlay(this.playbackList.results[index - 1].id);
            }

        } else {
            if (index === -1) {
                this.loadAndPlay(this.playbackList.results[0].id);

            } else if (index === this.playbackList.results.length - 1) {
                if (this.playbackList.options.offset < (this.playbackList.count - this.playbackList.options.limit)) {
                    this.searchCalls(Object.assign({}, this.playbackList.options, {
                        offset: this.playbackList.options.offset + this.playbackList.options.limit,
                    }));

                } else if (this.playbackRefreshing) {
                    this.stopPlaybackMode();

                    if (this.config.playbackGoesLive) {
                        this.startLivefeed();
                    }

                } else {
                    this.playbackRefreshing = true;
                    this.searchCalls(this.playbackList.options);
                }

            } else {
                this.loadAndPlay(this.playbackList.results[index + 1].id);
            }
        }
    }

    private playOscillatorSequence(seq: RdioScannerOscillatorData[]): Promise<void> {
        return new Promise((resolve) => {
            const context = this.oscillatorContext;

            if (!context || !seq) {
                resolve();

                return;
            }

            const gn = context.createGain();

            gn.gain.value = .1;

            gn.connect(context.destination);

            seq.forEach((data, index) => {
                const osc = context.createOscillator();

                osc.connect(gn);

                osc.frequency.value = data.frequency;

                osc.type = data.type;

                if (index === seq.length - 1) {
                    osc.onended = () => resolve();
                }

                osc.start(context.currentTime + data.begin);

                osc.stop(context.currentTime + data.end);
            });
        });
    }

    private readLivefeedMap(): void {
        try {
            let lfm: { [key: number]: { [key: number]: boolean } } = {};

            let store = window?.localStorage?.getItem(`${RdioScannerService.LOCAL_STORAGE_KEY_LFM}-${this.instanceId}`);

            if (store !== null) {
                lfm = JSON.parse(store);

            } else {
                store = window?.localStorage?.getItem(RdioScannerService.LOCAL_STORAGE_KEY_LEGACY);

                if (store !== null) {
                    lfm = JSON.parse(store);
                }
            }

            Object.keys(lfm ?? {}).forEach((sys: string) => {
                Object.keys(lfm[+sys]).forEach((tg) => {
                    if (!this.livefeedMap[+sys]) this.livefeedMap[+sys] = {};
                    if (!this.livefeedMap[+sys][+tg]) this.livefeedMap[+sys][+tg] = {} as RdioScannerLivefeed;
                    this.livefeedMap[+sys][+tg].active = lfm[+sys][+tg];
                });
            });

        } catch (_) {
            //
        }
    }

    private rebuildCategories(): void {
        this.categories = Object.keys(this.config.groups || []).map((label) => {
            const allOff = Object.keys(this.config.groups[label]).map((sys) => +sys)
                .every((sys: number) => this.config.groups[label] && this.config.groups[label][sys]
                    .every((tg) => this.livefeedMap[sys] && !this.livefeedMap[sys][tg].active));

            const allOn = Object.keys(this.config.groups[label]).map((sys) => +sys)
                .every((sys: number) => this.config.groups[label] && this.config.groups[label][sys]
                    .every((tg) => this.livefeedMap[sys] && this.livefeedMap[sys][tg].active));

            const status = allOff ? RdioScannerCategoryStatus.Off : allOn ? RdioScannerCategoryStatus.On : RdioScannerCategoryStatus.Partial;

            return { label, status, type: RdioScannerCategoryType.Group };
        })

        this.categories.sort((a, b) => a.label.localeCompare(b.label));
    }

    private rebuildLivefeedMap(): void {
        const lfm = this.config.systems.reduce((sysMap, sys) => {
            sysMap[sys.id] = sys.talkgroups.reduce((tgMap, tg) => {
                const group = this.categories.find((cat) => tg.groups.includes(cat.label));
                const tag = this.categories.find((cat) => cat.label === tg.tag);

                // Check if this talkgroup exists in the saved livefeed map
                // Use explicit undefined check to handle falsy values correctly
                const existsInSavedMap = this.livefeedMap[sys.id] !== undefined && 
                                        this.livefeedMap[sys.id][tg.id] !== undefined;

                tgMap[tg.id] = existsInSavedMap
                    ? this.livefeedMap[sys.id][tg.id]
                    : {
                        // NEW FIX: Default to inactive (false) for new talkgroups/systems
                        // Users must manually enable them in Channel Select
                        active: false,
                    } as RdioScannerLivefeed;

                return tgMap;
            }, sysMap[sys.id] || {} as { [key: number]: RdioScannerLivefeed });
            return sysMap;
        }, {} as RdioScannerLivefeedMap);

        if (this.livefeedMapPriorToHoldSystem != null) {
            this.livefeedMapPriorToHoldSystem = lfm;
        } else if (this.livefeedMapPriorToHoldTalkgroup != null) {
            this.livefeedMapPriorToHoldTalkgroup = lfm;
        } else {
            this.livefeedMap = lfm;
        }

        this.saveLivefeedMap();

        this.rebuildCategories();
    }

    reconnectWebsocket(): void {
        if (this.isReconnecting) {
            return;
        }

        this.isReconnecting = true;
        this.closeWebsocket();

        // Small delay to ensure the previous connection is fully closed
        timer(500).subscribe(() => {
            this.openWebsocket();
        });
    }

    private saveLivefeedMap(): void {
        const lfm = Object.keys(this.livefeedMap).reduce((sysMap: { [key: number]: { [key: number]: boolean } }, sys: string) => {
            sysMap[+sys] = Object.keys(this.livefeedMap[+sys]).reduce((tgMap: { [key: number]: boolean }, tg: string) => {
                tgMap[+tg] = this.livefeedMap[+sys][+tg].active;
                return tgMap;
            }, {});
            return sysMap;
        }, {});

        window?.localStorage?.setItem(`${RdioScannerService.LOCAL_STORAGE_KEY_LFM}-${this.instanceId}`, JSON.stringify(lfm));
    }

    private sendtoWebsocket(command: string, payload?: unknown, flags?: string): void {
        if (this.websocket?.readyState === 1) {
            const message: unknown[] = [command];

            if (payload) {
                message.push(payload);
            }

            if (flags !== null && flags !== undefined) {
                message.push(flags);
            }

            this.websocket.send(JSON.stringify(message));
        }
    }


    private transformCall(call: RdioScannerCall): RdioScannerCall {
        if (call && Array.isArray(this.config?.systems)) {
            call.systemData = this.config.systems.find((system) => system.id === call.system);

            if (Array.isArray(call.systemData?.talkgroups)) {
                call.talkgroupData = call.systemData?.talkgroups.find((talkgroup) => talkgroup.id === call.talkgroup);
            }

            if (call.talkgroupData?.frequency) {
                call.frequency = call.talkgroupData.frequency;
            }

            call.groupsData = this.config.groupsData.filter((gd) => call.talkgroupData?.groups.some((l) => l === gd.label));

            call.tagData = this.config.tagsData.find((td) => td.label === call.talkgroupData?.tag);

            // Resolve site data if call has a site (siteRef) and system has sites
            if (call.site && call.systemData && (call.systemData as any).sites) {
                const sites = (call.systemData as any).sites;
                if (Array.isArray(sites)) {
                    const site = sites.find((s: any) => s.siteRef === call.site);
                    if (site) {
                        call.siteData = {
                            id: site.id || 0,
                            label: site.label || '',
                            siteRef: site.siteRef || call.site,
                            systemId: call.systemData.id
                        };
                    }
                }
            }
        }

        return call;
    }

    private restoreHoldStates(call: RdioScannerCall): void {
        if (this.pendingHoldSys) {
            this.holdSystem({ resubscribe: false });
            this.pendingHoldSys = false;
        }

        if (this.pendingHoldTg) {
            this.holdTalkgroup({ resubscribe: false });
            this.pendingHoldTg = false;
        }
    }
}
