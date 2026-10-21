/**
 * The Alt shortcuts of the forms and the main menu (Alt+S saves the marked
 * rows, Alt+C copies a row, Alt+A shows the admin section, ...).
 *
 * `keys` maps a letter to its action, for example `{ s: save, c: copy }`.
 * An action runs when its letter is pressed with Alt (Option on a Mac) and
 * without Ctrl, Cmd or Shift, so AltGr (Ctrl and Alt together) still types
 * its characters. The letter is the one on the key, as the tooltips name it,
 * on AZERTY or Dvorak too; where the key types no letter a to z (Option on
 * a Mac, a Cyrillic layout) it is the letter at that place on a US keyboard.
 * Returns the function that removes the shortcuts again, for an $effect to
 * return.
 *
 * Every key press is judged by itself. A library that kept a list of the
 * keys held down, and ran a shortcut only when that list was exactly Alt and
 * its letter, never saw a key let go while a message box was open, and from
 * then on ignored every shortcut until the window lost focus.
 */
export function altShortcuts(keys) {
	const onKeydown = (e) => {
		if (!e.altKey || e.ctrlKey || e.metaKey || e.shiftKey || e.isComposing) return;
		const action = keys[letterOf(e)];
		if (!action) return;
		e.preventDefault();
		action();
	};
	document.addEventListener('keydown', onKeydown);
	return () => document.removeEventListener('keydown', onKeydown);
}

/** The letter of a key press, a to z, or '' for any other key. */
function letterOf(e) {
	if (/^[a-z]$/i.test(e.key ?? '')) return e.key.toLowerCase();
	const code = /^Key([A-Z])$/.exec(e.code ?? '');
	return code ? code[1].toLowerCase() : '';
}
