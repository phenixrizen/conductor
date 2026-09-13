// JSON.parse discards duplicate keys. Reject them before parsing a consequential
// plan so the preview cannot conceal a second source, scope or command field.
export function parseStrictJSON(input: string): unknown {
  if (new TextEncoder().encode(input).length > 1048576) throw new Error('The JSON file exceeds 1 MiB.');
  let at=0;
  const skip=()=>{while(/[\t\r\n ]/.test(input[at]??'!'))at++;};
  const string=():string=>{
    const start=at++;
    while(at<input.length){const c=input[at++];if(c==='"')return JSON.parse(input.slice(start,at)) as string;if(c==='\\')at++;}
    throw new Error('The JSON string is incomplete.');
  };
  function value(depth:number):void {
    if(depth>32)throw new Error('The JSON nesting exceeds 32 levels.');skip();const c=input[at];
    if(c==='"'){string();return;}
    if(c==='{'||c==='['){at++;skip();const close=c==='{'?'}':']';const seen=new Set<string>();if(input[at]===close){at++;return;}
      for(;;){skip();if(c==='{'){if(input[at]!=='"')throw new Error('Expected a JSON field name.');const key=string();if(seen.has(key))throw new Error(`Duplicate JSON field: ${key.slice(0,128)}`);seen.add(key);skip();if(input[at++]!==':')throw new Error('Expected a JSON field value.');}value(depth+1);skip();if(input[at]===close){at++;return;}if(input[at++]!==',')throw new Error('Expected a JSON separator.');}
    }
    const match=input.slice(at).match(/^(?:true|false|null|-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?)/);if(!match)throw new Error('Invalid JSON value.');at+=match[0].length;
  }
  value(0);skip();if(at!==input.length)throw new Error('Unexpected content after the JSON document.');return JSON.parse(input);
}
