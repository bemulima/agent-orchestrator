import { writeFileSync, renameSync, openSync, fsyncSync, closeSync } from "node:fs";
import path from "node:path";
// Matches domain.TaskAttemptStatus; this is the runner execution audit, not a
// second orchestration scheduler. Failed execution audits never become success.
export type ExecutionStatus = "created" | "admitted" | "running" | "verification" | "completed" | "failed" | "cancelled" | "timed_out" | "sandbox_lost" | "infrastructure_unknown";
export class ExecutionLifecycle {
  readonly events: Array<{status:ExecutionStatus;at:string}> = [];
  private terminal = false;
  constructor(readonly file:string, readonly identity:string, readonly attempt:number, readonly privateRoot:string) {this.move("created");}
  move(status:ExecutionStatus):void {
    if(this.terminal)throw new Error("EXECUTION_TERMINAL_STATE_IMMUTABLE");
    const prior=this.events.at(-1)?.status;
    const next:Record<string,string>={created:"admitted",admitted:"running",running:"verification",verification:"completed"};
    if(prior && ["admitted","running","verification","completed"].includes(status) && next[prior]!==status)throw new Error("EXECUTION_TRANSITION_DENIED");
    this.events.push({status,at:new Date().toISOString()});
    this.terminal=["completed","failed","cancelled","timed_out","sandbox_lost","infrastructure_unknown"].includes(status);
    const tmp=this.file+".tmp";writeFileSync(tmp,JSON.stringify({execution_id:this.identity,attempt:this.attempt,status,private_root:this.privateRoot,events:this.events}),{mode:0o600});
    const fd=openSync(tmp,"r");try{fsyncSync(fd);}finally{closeSync(fd);}renameSync(tmp,this.file);
    const parent=openSync(path.dirname(this.file),"r");try{fsyncSync(parent);}finally{closeSync(parent);}
  }
}
export async function checkpoint(phase:string,signal:AbortSignal):Promise<void> {
  // Trusted test observer only. No checkpoint path enters SDK/model environment.
  const gate=process.env.CDO_LIFECYCLE_GATE;
  if(!gate)return;
  if(process.env.CDO_SANDBOX_EXECUTION_MODE!=="hardened-certification")throw new Error("LIFECYCLE_GATE_ADMISSION_DENIED");
  const {existsSync}=await import("node:fs");
  writeFileSync(path.join(gate,phase+".ready"),new Date().toISOString(),{mode:0o600});
  if(process.env.CDO_LIFECYCLE_PHASE!==phase)return;
  while(!existsSync(path.join(gate,phase+".release"))){signal.throwIfAborted();await new Promise(resolve=>setTimeout(resolve,25));}
  signal.throwIfAborted();
}
