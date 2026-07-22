/*
 * Copyright (C) 2025 Thinline Dynamic Solutions
 */

import { Injectable } from '@angular/core';
import { NwsSevereAlert } from './nws.service';

/**
 * Reads severe weather alerts aloud using the browser's built-in
 * text-to-speech engine (Web Speech API). No server/API key required.
 */
@Injectable({
    providedIn: 'root',
})
export class WeatherAlertTtsService {
    private queue: string[] = [];
    private speaking = false;

    /** Whether the current browser supports speech synthesis at all. */
    isSupported(): boolean {
        return typeof window !== 'undefined' && 'speechSynthesis' in window;
    }

    /** Speak one or more severe alerts, queuing them so they don't overlap. */
    speakAlerts(alerts: NwsSevereAlert[]): void {
        if (!alerts.length) {
            return;
        }
        for (const alert of alerts) {
            this.speak(this.buildSpeechText(alert));
        }
    }

    /** Speak a single line of text (used for the "Test TTS" preview button). */
    speak(text: string): void {
        if (!this.isSupported() || !text) {
            return;
        }
        this.queue.push(text);
        this.pumpQueue();
    }

    /** Stop anything currently being read and clear the queue. */
    stop(): void {
        this.queue = [];
        this.speaking = false;
        if (this.isSupported()) {
            window.speechSynthesis.cancel();
        }
    }

    private buildSpeechText(alert: NwsSevereAlert): string {
        const parts = [alert.event || 'Weather Alert'];
        if (alert.area) {
            parts.push(`for ${alert.area}`);
        }
        if (alert.headline) {
            parts.push(alert.headline);
        }
        return parts.join('. ');
    }

    private pumpQueue(): void {
        if (this.speaking || !this.queue.length || !this.isSupported()) {
            return;
        }
        const text = this.queue.shift();
        if (!text) {
            return;
        }
        this.speaking = true;
        const utterance = new SpeechSynthesisUtterance(text);
        utterance.rate = 1;
        utterance.pitch = 1;
        utterance.volume = 1;
        const finish = () => {
            this.speaking = false;
            this.pumpQueue();
        };
        utterance.onend = finish;
        utterance.onerror = finish;
        window.speechSynthesis.speak(utterance);
    }
}
