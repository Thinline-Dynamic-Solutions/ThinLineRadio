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

import { Component, ElementRef, ViewChild, ChangeDetectionStrategy, ChangeDetectorRef, OnDestroy } from '@angular/core';
import { MatSnackBar } from '@angular/material/snack-bar';
import {
    CopilotMessage,
    CopilotStreamEvent,
    CopilotToolEvent,
    RdioScannerAdminService,
} from '../admin.service';

@Component({
    selector: 'rdio-scanner-admin-assistant',
    styleUrls: ['./assistant.component.scss'],
    templateUrl: './assistant.component.html',
    changeDetection: ChangeDetectionStrategy.Eager,
    standalone: false
})
export class RdioScannerAdminAssistantComponent implements OnDestroy {
    messages: CopilotMessage[] = [];
    input = '';
    loading = false;
    error: string | null = null;
    statusText = '';
    activeTools: CopilotToolEvent[] = [];
    awaitingConfirm = false;

    readonly suggestions = [
        'What should I check first on this server?',
        'Show recent error logs from the last 24 hours',
        'Are there any active system health alerts?',
        'Audit talkgroup tags for mismatches',
        'How do I configure Stripe webhooks?',
    ];

    private abort?: AbortController;
    private softTimeoutId?: ReturnType<typeof setTimeout>;

    @ViewChild('thread') private threadEl?: ElementRef<HTMLDivElement>;
    @ViewChild('inputBox') private inputEl?: ElementRef<HTMLTextAreaElement>;

    constructor(
        private adminService: RdioScannerAdminService,
        private snackBar: MatSnackBar,
        private cdr: ChangeDetectorRef,
    ) {}

    ngOnDestroy(): void {
        this.cancelInFlight();
    }

    useSuggestion(text: string): void {
        this.input = text;
        this.inputEl?.nativeElement.focus();
    }

    clearChat(): void {
        if (!this.messages.length) {
            return;
        }
        if (!confirm('Are you sure you want to clear the assistant chat?')) {
            return;
        }
        this.cancelInFlight();
        this.messages = [];
        this.error = null;
        this.awaitingConfirm = false;
        this.activeTools = [];
        this.statusText = '';
    }

    cancel(): void {
        this.cancelInFlight();
        this.loading = false;
        this.statusText = '';
        this.activeTools = [];
        this.error = 'Cancelled';
        this.cdr.markForCheck();
    }

    confirmApply(): void {
        if (this.loading) {
            return;
        }
        this.awaitingConfirm = false;
        this.input = 'Yes, confirmed — proceed with confirmed=true and apply the change.';
        void this.send();
    }

    cancelApply(): void {
        if (this.loading) {
            return;
        }
        this.awaitingConfirm = false;
        this.input = 'Cancel — do not apply the change.';
        void this.send();
    }

    async send(): Promise<void> {
        const text = this.input.trim();
        if (!text || this.loading) {
            return;
        }

        this.error = null;
        this.awaitingConfirm = false;
        this.messages = [...this.messages, { role: 'user', content: text }];
        this.input = '';
        this.loading = true;
        this.statusText = 'Starting…';
        this.activeTools = [];
        this.scrollToBottom();

        this.abort = new AbortController();
        this.softTimeoutId = setTimeout(() => {
            if (this.loading) {
                this.snackBar.open('Still working — you can Cancel if this seems stuck.', 'OK', { duration: 5000 });
            }
        }, 180_000);

        const historyForApi = this.messages.map(m => ({ role: m.role, content: m.content }));
        const toolTranscript: CopilotToolEvent[] = [];

        try {
            const res = await this.adminService.copilotChatStream(
                historyForApi,
                (ev: CopilotStreamEvent) => this.onStreamEvent(ev, toolTranscript),
                this.abort.signal,
            );
            if (res.message?.content) {
                const content = res.message.content;
                this.messages = [
                    ...this.messages,
                    {
                        role: 'assistant',
                        content,
                        tools: toolTranscript.length ? [...toolTranscript] : undefined,
                    },
                ];
                if (!this.awaitingConfirm && /\b(confirm|go ahead|shall i|reply yes|confirmed=true)\b/i.test(content)) {
                    this.awaitingConfirm = true;
                }
            }
            if (res.toolsUsed?.length && !toolTranscript.length) {
                this.snackBar.open(`Used tools: ${res.toolsUsed.join(', ')}`, 'OK', { duration: 4000 });
            }
        } catch (e: unknown) {
            const msg = e instanceof Error ? e.message : 'Assistant request failed';
            if (msg !== 'Cancelled') {
                this.error = msg;
                this.snackBar.open(msg, 'Dismiss', { duration: 6000 });
            }
        } finally {
            if (this.softTimeoutId) {
                clearTimeout(this.softTimeoutId);
                this.softTimeoutId = undefined;
            }
            this.abort = undefined;
            this.loading = false;
            this.statusText = '';
            this.activeTools = [];
            this.scrollToBottom();
            this.cdr.markForCheck();
        }
    }

    onKeydown(event: KeyboardEvent): void {
        if (event.key === 'Enter' && !event.shiftKey) {
            event.preventDefault();
            void this.send();
        }
    }

    private onStreamEvent(ev: CopilotStreamEvent, toolTranscript: CopilotToolEvent[]): void {
        switch (ev.type) {
            case 'status':
                this.statusText = ev.message || 'Working…';
                if (ev.needsConfirm) {
                    this.awaitingConfirm = true;
                }
                break;
            case 'tool_start': {
                const name = ev.tool || 'tool';
                this.statusText = ev.message || `Running ${name}…`;
                this.activeTools = [
                    ...this.activeTools.filter(t => t.name !== name || t.status === 'done'),
                    { name, status: 'running', summary: ev.message },
                ];
                const existing = toolTranscript.find(t => t.name === name && t.status === 'running');
                if (!existing) {
                    toolTranscript.push({ name, status: 'running' });
                }
                break;
            }
            case 'tool_end': {
                const name = ev.tool || 'tool';
                this.activeTools = this.activeTools.map(t =>
                    t.name === name && t.status === 'running'
                        ? { ...t, status: 'done' as const, summary: ev.summary }
                        : t,
                );
                const idx = toolTranscript.map((t, i) => ({ t, i }))
                    .reverse()
                    .find(x => x.t.name === name)?.i;
                if (idx !== undefined) {
                    toolTranscript[idx] = { name, status: 'done', summary: ev.summary };
                } else {
                    toolTranscript.push({ name, status: 'done', summary: ev.summary });
                }
                if (ev.needsConfirm) {
                    this.awaitingConfirm = true;
                }
                break;
            }
            case 'message':
            case 'done':
                if (ev.needsConfirm) {
                    this.awaitingConfirm = true;
                } else if (ev.type === 'done') {
                    // Keep awaitingConfirm if tools already signaled it this turn.
                }
                break;
            default:
                break;
        }
        this.scrollToBottom();
        this.cdr.markForCheck();
    }

    private cancelInFlight(): void {
        if (this.softTimeoutId) {
            clearTimeout(this.softTimeoutId);
            this.softTimeoutId = undefined;
        }
        this.abort?.abort();
        this.abort = undefined;
    }

    private scrollToBottom(): void {
        setTimeout(() => {
            const el = this.threadEl?.nativeElement;
            if (el) {
                el.scrollTop = el.scrollHeight;
            }
        }, 50);
    }
}
