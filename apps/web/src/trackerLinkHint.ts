import { isHex } from './graphs';
const hintKey='conductor.requestedTrackerLink';
// Preserve only an opaque navigation hint across the fixed OIDC redirect. It is
// never a credential, scope selection, data cache or permission to read a link.
export function captureTrackerLinkHint(){
 const query=new URLSearchParams(window.location.search),ids=query.getAll('tracker-link');
 if(ids.length===1&&isHex(ids[0],32)){try{sessionStorage.setItem(hintKey,ids[0]);}catch{/* Navigation still works through explicit ID entry. */}}
}
export function readTrackerLinkHint(){try{const id=sessionStorage.getItem(hintKey);return isHex(id,32)?id:'';}catch{return '';}}
export function clearTrackerLinkHint(id:string){try{if(sessionStorage.getItem(hintKey)===id)sessionStorage.removeItem(hintKey);}catch{/* Only an optional navigation hint is affected. */}}
