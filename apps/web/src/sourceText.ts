// Retained source is data. Show control and bidirectional formatting characters
// explicitly while preserving normal source whitespace.
export function visibleControls(text: string) {return text.replace(/[\u0000-\u0008\u000b-\u001f\u007f-\u009f\u202a-\u202e\u2066-\u2069]/g,c=>`\\u${c.charCodeAt(0).toString(16).padStart(4,'0')}`);}
