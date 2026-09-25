import os,sys,tty,time
log=open(sys.argv[1],'w',buffering=1)
if len(sys.argv)>2: sys.stdout.write('\x1b[?2004h'); sys.stdout.flush()  # bracketed paste on
tty.setraw(0); t0=time.time()
while True:
    b=os.read(0,65536)
    log.write('%.4f %d %r\n'%(time.time()-t0,len(b),b))
