import { Component, EventEmitter, Input, OnDestroy, OnInit, Output } from '@angular/core';
import { Subscription } from 'rxjs';
import { MatSnackBar } from '@angular/material/snack-bar';
import { RdioScannerTranscript } from '../rdio-scanner';
import { AlertsService } from '../alerts/alerts.service';
import { TranscriptReviewService } from './transcript-review.service';
import { trainingSubmitIssues } from './transcript-training-rules';

@Component({
    selector: 'rdio-scanner-transcript-review',
    templateUrl: './transcript-review.component.html',
    styleUrls: ['./transcript-review.component.scss'],
})
export class RdioScannerTranscriptReviewComponent implements OnInit, OnDestroy {
    @Input() collectorConfigured = false;
    @Output() requestCollectorSetup = new EventEmitter<void>();

    transcripts: RdioScannerTranscript[] = [];
    loading = false;
    error = '';

    offset = 0;
    readonly limit = 50;
    hasMore = false;

    // Admin edit state
    editingCallId: number | null = null;
    editText = '';
    editSaving = false;
    editApproving = false;
    editAudioSrc = '';
    editAudioLoading = false;
    editAudioSeconds: number | null = null;
    serverIssues: string[] = [];
    showTrainingTips = false;
    private editAudioObjectUrl: string | null = null;

    private pin = '';
    private loadSub?: Subscription;

    constructor(
        private alertsService: AlertsService,
        private reviewService: TranscriptReviewService,
        private snackBar: MatSnackBar,
    ) {}

    ngOnInit(): void {
        const raw = window?.localStorage?.getItem('rdio-scanner-pin');
        this.pin = raw ? window.atob(raw) : '';
        this.load();
    }

    ngOnDestroy(): void {
        this.loadSub?.unsubscribe();
        this.revokeEditAudio();
    }

    load(): void {
        this.loading = true;
        this.error = '';
        this.loadSub?.unsubscribe();
        this.loadSub = this.alertsService.getTranscripts(this.limit, this.offset, this.pin || undefined)
            .subscribe({
                next: (items) => {
                    this.transcripts = (items || []).map((t: any) => ({ ...t, transcript: t.transcript || '' }));
                    this.hasMore = this.transcripts.length >= this.limit;
                    this.loading = false;
                },
                error: (err) => {
                    this.error = err?.message || 'Failed to load transcripts';
                    this.transcripts = [];
                    this.loading = false;
                },
            });
    }

    prevPage(): void {
        this.offset = Math.max(0, this.offset - this.limit);
        this.load();
    }

    nextPage(): void {
        if (!this.hasMore) return;
        this.offset += this.limit;
        this.load();
    }

    formatTime(ts: number): string {
        if (!ts) return '';
        return new Date(ts).toLocaleString();
    }

    channelLabel(t: RdioScannerTranscript): string {
        const sys = t.systemLabel || `System ${t.systemId}`;
        const tg = t.talkgroupLabel || t.talkgroupName || `Talkgroup ${t.talkgroupId}`;
        return `${sys} / ${tg}`;
    }

    trackById(_i: number, t: RdioScannerTranscript): number | string {
        return t.callId ?? _i;
    }

    cardIssues(t: RdioScannerTranscript): string[] {
        if (t.trainingReviewStatus === 'submitted' || this.editingCallId === t.callId) {
            return [];
        }
        return trainingSubmitIssues(t.transcript || '', null);
    }

    // ── Admin edit ────────────────────────────────────────────────────────────

    toggleEdit(t: RdioScannerTranscript): void {
        if (t.trainingReviewStatus === 'submitted') {
            return;
        }
        if (this.editingCallId === t.callId) {
            this.cancelEdit();
            return;
        }
        this.cancelEdit();
        this.editingCallId = t.callId ?? null;
        this.editText = t.reviewedTranscript?.trim() || t.transcript || '';
        if (t.callId != null) {
            this.loadEditAudio(t.callId);
        }
    }

    cancelEdit(): void {
        this.editingCallId = null;
        this.editText = '';
        this.serverIssues = [];
        this.editAudioSeconds = null;
        this.revokeEditAudio();
    }

    get editIssues(): string[] {
        const live = trainingSubmitIssues(this.editText, this.editAudioSeconds);
        const liveHasDuration = live.some((issue) =>
            issue.startsWith('This clip is') || issue.startsWith('The audio length'));
        const extra = this.serverIssues.filter((issue) => {
            if (live.includes(issue)) {
                return false;
            }
            if (liveHasDuration && (issue.startsWith('This clip is') || issue.startsWith('The audio length'))) {
                return false;
            }
            return true;
        });
        return live.concat(extra);
    }

    onEditTextChange(): void {
        this.serverIssues = this.serverIssues.filter((issue) =>
            issue.startsWith('This clip is') || issue.startsWith('The audio length'));
    }

    onEditAudioReady(event: Event): void {
        const el = event.target as HTMLAudioElement;
        const seconds = el?.duration;
        this.editAudioSeconds = Number.isFinite(seconds) ? seconds : null;
    }

    saveDraft(): void {
        if (this.editingCallId == null || !this.editText.trim()) return;
        this.editSaving = true;
        this.reviewService.save(this.editingCallId, this.editText.trim())
            .then(() => {
                this.snackBar.open('Draft saved', '', { duration: 2500 });
            })
            .catch((e: any) => {
                this.snackBar.open(e?.error?.error || 'Save failed', '', { duration: 5000 });
            })
            .finally(() => { this.editSaving = false; });
    }

    approve(): void {
        if (this.editingCallId == null || !this.editText.trim() || this.editIssues.length) return;
        if (!this.collectorConfigured) {
            this.snackBar.open('Connecting to transcript collector…', '', { duration: 3000 });
            this.requestCollectorSetup.emit();
            return;
        }
        this.editApproving = true;
        const approvedText = this.editText.trim().toUpperCase();
        this.reviewService.approve(this.editingCallId, approvedText)
            .then((res) => {
                this.snackBar.open(res.message || 'Approved & sent', '', { duration: 4000 });
                this.cancelEdit();
                this.load();
            })
            .catch((e: any) => {
                const issues = e?.error?.issues;
                if (Array.isArray(issues) && issues.length) {
                    this.serverIssues = issues.map((issue: unknown) => String(issue));
                    this.snackBar.open('This clip was not sent. Fix the issues below.', '', { duration: 5000 });
                    return;
                }
                this.snackBar.open(e?.error?.error || 'Approve failed', '', { duration: 6000 });
            })
            .finally(() => { this.editApproving = false; });
    }

    private loadEditAudio(callId: number): void {
        this.revokeEditAudio();
        this.editAudioLoading = true;
        const url = this.reviewService.audioUrl(callId);
        const headers = this.reviewService.getAudioFetchHeaders();
        fetch(url, { headers })
            .then(r => { if (!r.ok) throw new Error(`HTTP ${r.status}`); return r.blob(); })
            .then(blob => {
                if (!blob.size) throw new Error('empty');
                this.editAudioObjectUrl = URL.createObjectURL(blob);
                this.editAudioSrc = this.editAudioObjectUrl;
                this.editAudioLoading = false;
            })
            .catch(() => {
                this.editAudioSrc = '';
                this.editAudioLoading = false;
            });
    }

    private revokeEditAudio(): void {
        if (this.editAudioObjectUrl) {
            URL.revokeObjectURL(this.editAudioObjectUrl);
            this.editAudioObjectUrl = null;
        }
        this.editAudioSrc = '';
        this.editAudioLoading = false;
        this.editAudioSeconds = null;
    }
}
