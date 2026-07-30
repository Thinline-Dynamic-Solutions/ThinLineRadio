/**
 * Lightweight node harness for call-nature saveEdit phrase commit behavior.
 * Mirrors RdioScannerAdminCallNaturesComponent.saveEdit / addPhraseFromInput.
 */
function addPhraseFromInput(state) {
  const text = (state.newPhraseText || '').trim();
  if (!text) return state;
  const phrase = text.toUpperCase();
  const phrases = [...(state.phrases || [])];
  if (!phrases.includes(phrase)) {
    phrases.push(phrase);
  }
  return { ...state, phrases, newPhraseText: '' };
}

function buildPayload(state) {
  // Commit pending phrase before save (the UI fix).
  state = addPhraseFromInput(state);
  return {
    label: (state.label || '').toUpperCase().trim(),
    phrases: (state.phrases || [])
      .map((p) => p.toUpperCase().trim())
      .filter((p) => p.length > 0),
  };
}

function assert(cond, msg) {
  if (!cond) throw new Error(msg);
}

// Case 1: typed phrase, Save without clicking Add
{
  const payload = buildPayload({
    label: 'SHOTS FIRED',
    phrases: ['SHOTS FIRED'],
    newPhraseText: 'gunshots heard',
  });
  assert(payload.phrases.includes('GUNSHOTS HEARD'), 'pending phrase must be committed on save');
  assert(payload.phrases.includes('SHOTS FIRED'), 'existing phrases kept');
}

// Case 2: empty pending box leaves phrases alone
{
  const payload = buildPayload({
    label: 'STRUCTURE FIRE',
    phrases: ['FIRE'],
    newPhraseText: '   ',
  });
  assert(payload.phrases.length === 1 && payload.phrases[0] === 'FIRE', 'blank input must not mutate');
}

// Case 3: duplicate pending phrase is not double-added
{
  const payload = buildPayload({
    label: 'ALARM',
    phrases: ['ALARM DROP'],
    newPhraseText: 'alarm drop',
  });
  assert(payload.phrases.length === 1, 'duplicate pending phrase must not duplicate');
}

console.log('call-natures frontend save harness: PASS');
