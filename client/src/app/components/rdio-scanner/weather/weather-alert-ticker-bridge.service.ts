/*
 * Copyright (C) 2025 Thinline Dynamic Solutions
 */

import { Injectable } from '@angular/core';
import { RdioScannerWeatherAlertTickerComponent } from './weather-alert-ticker.component';

/** Lets settings (and other panels) trigger the header weather ticker test. */
@Injectable()
export class WeatherAlertTickerBridgeService {
    private ticker: RdioScannerWeatherAlertTickerComponent | null = null;

    register(ticker: RdioScannerWeatherAlertTickerComponent): void {
        this.ticker = ticker;
    }

    unregister(ticker: RdioScannerWeatherAlertTickerComponent): void {
        if (this.ticker === ticker) {
            this.ticker = null;
        }
    }

    triggerTest(playSound: boolean, soundName?: string, speakTts?: boolean): boolean {
        if (!this.ticker) {
            return false;
        }
        this.ticker.showTestAlert(playSound, soundName, speakTts);
        return true;
    }
}
